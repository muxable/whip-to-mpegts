package whiptompegts

/*
#cgo pkg-config: libavformat libavutil
#include <libavformat/avformat.h>
#include <libavutil/log.h>
#include "demux.h"
*/
import "C"
import (
	"errors"
	"io"
	"os"
	"unsafe"

	"github.com/mattn/go-pointer"
	"github.com/pion/rtpio/pkg/rtpio"
	"github.com/pion/webrtc/v3"
)

type RTPDemuxer struct {
	avformatctx *C.AVFormatContext
	rtpin       rtpio.RTPReader
	rtpseq      *uint16 // used for debugging.
	rawin       io.Reader
}

func init() {
	C.av_log_set_level(56)
}

var (
	csdp              = C.CString("sdp")
	csdpflags         = C.CString("sdp_flags")
	ccustomio         = C.CString("custom_io")
	creorderqueuesize = C.CString("reorder_queue_size")
)

func NewRTPDemuxer(codec webrtc.RTPCodecParameters, in rtpio.RTPReader) (*RTPDemuxer, error) {
	sdpformat := C.av_find_input_format(csdp)
	if sdpformat == nil {
		return nil, errors.New("could not find sdp format")
	}

	avformatctx := C.avformat_alloc_context()
	if avformatctx == nil {
		return nil, errors.New("failed to create format context")
	}

	// initialize an RTP demuxer
	var opts *C.AVDictionary
	defer C.av_dict_free(&opts)
	if averr := C.av_dict_set(&opts, csdpflags, ccustomio, 0); averr < 0 {
		return nil, av_err("av_dict_set", averr)
	}
	if averr := C.av_dict_set_int(&opts, creorderqueuesize, C.int64_t(0), 0); averr < 0 {
		return nil, av_err("av_dict_set", averr)
	}

	sdpfile, err := NewTempSDP(codec)
	if err != nil {
		return nil, err
	}

	cfilename := C.CString(sdpfile.Name())
	defer C.free(unsafe.Pointer(cfilename))

	if averr := C.avformat_open_input(&avformatctx, cfilename, sdpformat, &opts); averr < 0 {
		return nil, av_err("avformat_open_input", averr)
	}

	buf := C.av_malloc(1500)
	if buf == nil {
		return nil, errors.New("failed to allocate buffer")
	}

	c := &RTPDemuxer{
		avformatctx: avformatctx,
		rtpin:       in,
	}

	avioctx := C.avio_alloc_context((*C.uchar)(buf), 1500, 1, pointer.Save(c), (*[0]byte)(C.cgoReadBufferFunc), (*[0]byte)(C.cgoWriteRTCPPacketFunc), nil)
	if avioctx == nil {
		return nil, errors.New("failed to allocate avio context")
	}

	avformatctx.pb = avioctx

	if averr := C.avformat_find_stream_info(avformatctx, nil); averr < 0 {
		return nil, av_err("avformat_find_stream_info", averr)
	}

	if err := sdpfile.Close(); err != nil {
		return nil, err
	}

	if err := os.Remove(sdpfile.Name()); err != nil {
		return nil, err
	}

	return c, nil
}

//export goReadBufferFunc
func goReadBufferFunc(opaque unsafe.Pointer, cbuf *C.uint8_t, bufsize C.int) C.int {
	d := pointer.Restore(opaque).(*RTPDemuxer)
	p, err := d.rtpin.ReadRTP()
	if err != nil {
		if err != io.EOF {
			return AVERROR(C.EIO)
		}
		return AVERROR_EOF
	}

	b, err := p.Marshal()
	if err != nil {
		return AVERROR(C.EINVAL)
	}

	d.rtpseq = &p.SequenceNumber

	if C.int(len(b)) > bufsize {
		return AVERROR(C.ENOMEM)
	}

	C.memcpy(unsafe.Pointer(cbuf), unsafe.Pointer(&b[0]), C.ulong(len(b)))

	return C.int(len(b))
}

//export goWriteRTCPPacketFunc
func goWriteRTCPPacketFunc(opaque unsafe.Pointer, buf *C.uint8_t, bufsize C.int) C.int {
	// this function is necessary: https://trac.ffmpeg.org/ticket/9670
	return bufsize
}
