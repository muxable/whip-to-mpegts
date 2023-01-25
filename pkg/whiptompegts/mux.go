package whiptompegts

/*
#cgo pkg-config: libavformat libavcodec
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include "demux.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"unsafe"
)

type MPEGTSMuxer struct {
	avformatctx *C.AVFormatContext
	avpkt       *C.AVPacket
}

func NewMPEGTSMuxer(demuxers []*RTPDemuxer) (*MPEGTSMuxer, error) {
	// creates a new mpeg-ts muxer sourced from the n demuxer streams

	avformatctx, err := C.avformat_alloc_context()
	if avformatctx == nil {
		return nil, fmt.Errorf("failed to create format context: %w", err)
	}

	avformatctx.oformat = C.av_guess_format(C.CString("mpegts"), nil, nil)
	if avformatctx.oformat == nil {
		return nil, errors.New("failed to guess format")
	}

	avformatctx.oformat.video_codec = C.AV_CODEC_ID_MPEG2VIDEO
	avformatctx.oformat.audio_codec = C.AV_CODEC_ID_MP2

	avformatctx.oformat.flags |= C.AVFMT_NOFILE

	// create the output streams
	for i, demuxer := range demuxers {
		avstream, err := C.avformat_new_stream(avformatctx, nil)
		if avstream == nil {
			return nil, fmt.Errorf("failed to create stream: %w", err)
		}

		avstream.index = C.int(i)
		avstream.id = C.int(i)

		// copy codec parameters from demuxer
		streams := (*[1 << 30]*C.AVStream)(unsafe.Pointer(demuxer.avformatctx.streams))[:avformatctx.nb_streams:avformatctx.nb_streams]
		avstream.codecpar = streams[0].codecpar
	}

	return &MPEGTSMuxer{
		avformatctx: avformatctx,
		avpkt:       C.av_packet_alloc(),
	}, nil
}

func (m *MPEGTSMuxer) Read(buf []byte) (int, error) {
	// read a packet from the muxer
	if err := C.av_read_frame(m.avformatctx, m.avpkt); err < 0 {
		return 0, av_err("av_read_frame", err)
	}

	// copy the packet into the buffer
	if int(m.avpkt.size) > len(buf) {
		return 0, errors.New("buffer too small")
	}

	C.memcpy(unsafe.Pointer(&buf[0]), unsafe.Pointer(m.avpkt.data), C.size_t(m.avpkt.size))

	return int(m.avpkt.size), nil
}

func (m *MPEGTSMuxer) Close() error {
	C.av_packet_free(&m.avpkt)
	C.avformat_free_context(m.avformatctx)
	return nil
}
