package whiptompegts

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type Server struct {
	pcs map[string]*webrtc.PeerConnection

	OnMPEGTSStream func(string, io.Reader)
}

func NewServer(f func(string, io.Reader)) *Server {
	return &Server{
		pcs:            make(map[string]*webrtc.PeerConnection),
		OnMPEGTSStream: f,
	}
}

func (s *Server) Handler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/"):]
	switch r.Method {
	case http.MethodGet:
		w.WriteHeader(http.StatusMethodNotAllowed)

	case http.MethodPost:
		// WHIP create
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
		})
		if err != nil {
			log.Printf("Failed to create peer connection: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			log.Printf("Connection State has changed %s", connectionState.String())
		})

		gatherComplete := webrtc.GatheringCompletePromise(pc)

		if err := pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  string(body),
		}); err != nil {
			log.Printf("Failed to set remote description: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			log.Printf("Failed to create answer: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		answerSDP, err := answer.Unmarshal()
		if err != nil {
			log.Printf("Failed to unmarshal answer: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		trackCount := len(answerSDP.MediaDescriptions)
		tracks := make(chan *webrtc.TrackRemote, trackCount)

		pc.OnTrack(func(tr *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			tracks <- tr
		})

		if err := pc.SetLocalDescription(answer); err != nil {
			log.Printf("Failed to set local description: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		<-gatherComplete

		pcid := uuid.NewString()

		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", fmt.Sprintf("http://%s/%s", r.Host, pcid))

		if _, err := w.Write([]byte(pc.LocalDescription().SDP)); err != nil {
			log.Printf("Failed to write response: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		s.pcs[pcid] = pc

		go func() {
			// start an ffmpeg demuxer
			demuxers := make([]*RTPDemuxer, trackCount)
			for i := 0; i < trackCount; i++ {
				tr := <-tracks
				demux, err := NewRTPDemuxer(tr.Codec(), &TrackReader{tr})
				if err != nil {
					log.Printf("Failed to create demuxer: %s", err)
					return
				}
				demuxers[i] = demux
			}

			muxer, err := NewMPEGTSMuxer(demuxers)
			if err != nil {
				log.Printf("Failed to create muxer: %s", err)
				return
			}

			s.OnMPEGTSStream(pcid, muxer)
			pc.Close()
			delete(s.pcs, pcid)
		}()

	case http.MethodPatch:
		if r.Header.Get("Content-Type") != "application/sdp" {
			// Trickle ICE/ICE restart not implemented
			panic("Not implemented")
		}

		pc, ok := s.pcs[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeAnswer,
			SDP:  string(body),
		}); err != nil {
			log.Printf("Failed to set remote description: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	case http.MethodDelete:
		pc, ok := s.pcs[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if err := pc.Close(); err != nil {
			log.Printf("Failed to close peer connection: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		delete(s.pcs, id)
	}
}

type TrackReader struct {
	Track *webrtc.TrackRemote
}

func (r *TrackReader) ReadRTP() (*rtp.Packet, error) {
	p, _, err := r.Track.ReadRTP()
	return p, err
}
