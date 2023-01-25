package whiptompegts

/*
#cgo pkg-config: libavformat libavcodec
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include "mux.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"unsafe"

	"github.com/mattn/go-pointer"
)

type MPEGTSMuxer struct {
	sync.Mutex

	avformatctx *C.AVFormatContext

	outCh chan []byte
}

func NewMPEGTSMuxer(demuxers []*RTPDemuxer) (*MPEGTSMuxer, error) {
	// creates a new mpeg-ts muxer sourced from the n demuxer streams

	aviobuf := C.av_malloc(C.size_t(4096))
	if aviobuf == nil {
		return nil, errors.New("failed to allocate io buffer")
	}

	m := &MPEGTSMuxer{outCh: make(chan []byte, 1)}

	avioctx := C.avio_alloc_context(
		(*C.uchar)(aviobuf),
		C.int(4096),
		1,
		pointer.Save(m),
		nil,
		(*[0]byte)(C.cgoWritePacketFunc),
		nil,
	)
	if avioctx == nil {
		return nil, errors.New("failed to create io context")
	}

	of := C.av_guess_format(C.CString("mpegts"), nil, nil)

	if averr := C.avformat_alloc_output_context2(&m.avformatctx, of, nil, nil); averr < 0 {
		return nil, av_err("avformat_alloc_output_context2", averr)
	}

	m.avformatctx.oformat.flags |= C.AVFMT_NOFILE
	m.avformatctx.pb = avioctx
	m.avformatctx.flags |= C.AVFMT_FLAG_CUSTOM_IO
	m.avformatctx.oformat = of

	// create the output streams
	for i, demuxer := range demuxers {
		avstream, err := C.avformat_new_stream(m.avformatctx, nil)
		if avstream == nil {
			return nil, fmt.Errorf("failed to create stream: %w", err)
		}

		avstream.index = C.int(i)
		avstream.id = C.int(i)

		// copy codec parameters from demuxer
		streams := (*[1 << 30]*C.AVStream)(unsafe.Pointer(demuxer.avformatctx.streams))[:m.avformatctx.nb_streams:m.avformatctx.nb_streams]
		if averr := C.avcodec_parameters_copy(avstream.codecpar, streams[0].codecpar); averr < 0 {
			return nil, av_err("avcodec_parameters_copy", averr)
		}
	}

	// write thread
	if averr := C.avformat_write_header(m.avformatctx, nil); averr < 0 {
		return nil, av_err("avformat_write_header", averr)
	}

	// create a thread to read packets from the demuxers and write them to the muxer
	for index, demuxer := range demuxers {
		go func(index int, demuxer *RTPDemuxer) {
			avpkt := C.av_packet_alloc()
			if avpkt == nil {
				panic("failed to allocate packet")
			}
			defer C.av_packet_free(&avpkt)

			for {
				// read a packet from the demuxer
				if err := C.av_read_frame(demuxer.avformatctx, avpkt); err < 0 {
					if err == AVERROR_EOF {
						return
					}
					panic(av_err("av_read_frame", err))
				}

				// set the stream index
				avpkt.stream_index = C.int(index)

				// rescale ts
				instreams := (*[1 << 30]*C.AVStream)(unsafe.Pointer(demuxer.avformatctx.streams))[:demuxer.avformatctx.nb_streams:demuxer.avformatctx.nb_streams]
				outstreams := (*[1 << 30]*C.AVStream)(unsafe.Pointer(m.avformatctx.streams))[:m.avformatctx.nb_streams:m.avformatctx.nb_streams]
				C.av_packet_rescale_ts(avpkt, instreams[0].time_base, outstreams[0].time_base)
				avpkt.pos = -1

				log.Printf("dts %d pts %d", avpkt.dts, avpkt.pts)

				m.Lock()
				if averr := C.av_interleaved_write_frame(m.avformatctx, avpkt); averr < 0 {
					panic(av_err("av_interleaved_write_frame", averr))
				}
				m.Unlock()
			}
		}(index, demuxer)
	}

	return m, nil
}

//export goWritePacketFunc
func goWritePacketFunc(opaque unsafe.Pointer, buf *C.uint8_t, bufsize C.int) C.int {
	m := pointer.Restore(opaque).(*MPEGTSMuxer)
	m.outCh <- C.GoBytes(unsafe.Pointer(buf), bufsize)
	return bufsize
}

func (m *MPEGTSMuxer) Read(buf []byte) (int, error) {
	// read a packet from the muxer
	pkt, ok := <-m.outCh
	if !ok {
		return 0, io.EOF
	}
	return copy(buf, pkt), nil
}

func (m *MPEGTSMuxer) Close() error {
	C.av_write_trailer(m.avformatctx)
	C.avformat_free_context(m.avformatctx)
	return nil
}
