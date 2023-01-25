package whiptompegts

/*
#cgo pkg-config: libavformat
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
		if err < 0 {
			return nil, av_err("avformat_new_stream", err)
		}

		avstream.index = C.int(i)
		avstream.id = C.int(i)

		// copy codec parameters from demuxer
		avstream.codecpar = demuxer.avformatctx.streams[0].codecpar
	}

	return &MPEGTSMuxer{
		avformatctx: avformatctx,
	}, nil
}

func (m *MPEGTSMuxer) Read(buf []byte) error {
	// read a packet from the muxer
	avpacket := C.av_packet_alloc()
	if avpacket == nil {
		return errors.New("failed to allocate packet")
	}

	defer C.av_packet_free(&avpacket)

	if err := C.av_read_frame(m.avformatctx, avpacket); err < 0 {
		return av_err("av_read_frame", err)
	}

	// copy the packet into the buffer
	if int(avpacket.size) > len(buf) {
		return errors.New("buffer too small")
	}

	C.memcpy(unsafe.Pointer(&buf[0]), unsafe.Pointer(avpacket.data), C.size_t(avpacket.size))

	return nil
}
