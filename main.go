package webrtc

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Monibuca/engine/v3"
	"github.com/Monibuca/plugin-webrtc/v3/webrtc"

	"github.com/Monibuca/utils/v3"
	"github.com/pion/rtcp"
	. "github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var config = struct {
	ICEServers []string
	PublicIP   []string
	PortMin    uint16
	PortMax    uint16
	PLI        time.Duration
}{nil, nil, 0, 0, 2000}

// }{[]string{
// 	"stun:stun.ekiga.net",
// 	"stun:stun.ideasip.com",
// 	"stun:stun.schlund.de",
// 	"stun:stun.stunprotocol.org:3478",
// 	"stun:stun.voiparound.com",
// 	"stun:stun.voipbuster.com",
// 	"stun:stun.voipstunt.com",
// 	"stun:stun.voxgratia.org",
// 	"stun:stun.services.mozilla.com",
// 	"stun:stun.xten.com",
// 	"stun:stun.softjoys.com",
// 	"stun:stunserver.org",
// 	"stun:stun.schlund.de",
// 	"stun:stun.rixtelecom.se",
// 	"stun:stun.iptel.org",
// 	"stun:stun.ideasip.com",
// 	"stun:stun.fwdnet.net",
// 	"stun:stun.ekiga.net",
// 	"stun:stun01.sipphone.com",
// }}

// type udpConn struct {
// 	conn *net.UDPConn
// 	port int
// }
var (
	playWaitList WaitList
	reg_level    = regexp.MustCompile("profile-level-id=(4.+f)")
	api          *API
)

type WaitList struct {
	m map[string]*WebRTC
	l sync.Mutex
}

func (wl *WaitList) Set(k string, v *WebRTC) {
	wl.l.Lock()
	defer wl.l.Unlock()
	if wl.m == nil {
		wl.m = make(map[string]*WebRTC)
	}
	wl.m[k] = v
}
func (wl *WaitList) Get(k string) *WebRTC {
	wl.l.Lock()
	defer wl.l.Unlock()
	defer delete(wl.m, k)
	return wl.m[k]
}
func init() {
	engine.InstallPlugin(&engine.PluginConfig{
		Config: &config,
		Name:   "WebRTC",
		Run:    run,
	})
}

type WebRTC struct {
	*PeerConnection
}

func (rtc *WebRTC) Publish(streamPath string) bool {
	if _, err := rtc.AddTransceiverFromKind(RTPCodecTypeVideo); err != nil {
		if err != nil {
			utils.Println("AddTransceiverFromKind video", err)
			return false
		}
	}
	if _, err := rtc.AddTransceiverFromKind(RTPCodecTypeAudio); err != nil {
		if err != nil {
			utils.Println("AddTransceiverFromKind audio", err)
			return false
		}
	}
	stream := &engine.Stream{
		Type:       "WebRTC",
		StreamPath: streamPath,
	}
	if stream.Publish() {
		rtc.OnICEConnectionStateChange(func(connectionState ICEConnectionState) {
			utils.Printf("%s Connection State has changed %s ", streamPath, connectionState.String())
			switch connectionState {
			case ICEConnectionStateDisconnected, ICEConnectionStateFailed:
				stream.Close()
			}
		})
		//f, _ := os.OpenFile("resource/live/rtc.h264", os.O_TRUNC|os.O_WRONLY, 0666)
		rtc.OnTrack(func(track *TrackRemote, receiver *RTPReceiver) {
			if codec := track.Codec(); track.Kind() == RTPCodecTypeAudio {
				var at *engine.RTPAudio
				switch codec.PayloadType {
				case 8:
					at = stream.NewRTPAudio(7)
					at.SoundRate = 8000
					at.SoundSize = 16
					at.Channels = 1
					at.ExtraData = []byte{(at.CodecID << 4) | (1 << 1)}
				case 0:
					at = stream.NewRTPAudio(8)
					at.SoundRate = 8000
					at.SoundSize = 16
					at.Channels = 1
					at.ExtraData = []byte{(at.CodecID << 4) | (1 << 1)}
				default:
					return
				}
				for {
					b := make([]byte, 1460)
					if i, _, err := track.Read(b); err == nil {
						at.Push(b[:i])
					} else {
						return
					}
				}
			} else {
				go func() {
					ticker := time.NewTicker(time.Millisecond * config.PLI)
					for {
						select {
						case <-ticker.C:
							if rtcpErr := rtc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}}); rtcpErr != nil {
								fmt.Println(rtcpErr)
							}
						case <-stream.Done():
							return
						}
					}
				}()
				vt := stream.NewRTPVideo(7)
				for {
					b := make([]byte, 1460)
					if i, _, err := track.Read(b); err == nil {
						vt.Push(b[:i])
					} else {
						return
					}
				}
			}

		})
	} else {
		return false
	}
	return true
}
func (rtc *WebRTC) GetAnswer() ([]byte, error) {
	// Sets the LocalDescription, and starts our UDP listeners
	answer, err := rtc.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}
	gatherComplete := GatheringCompletePromise(rtc.PeerConnection)
	if err := rtc.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	<-gatherComplete
	if bytes, err := json.Marshal(rtc.LocalDescription()); err != nil {
		utils.Println(err)
		return bytes, err
	} else {
		return bytes, nil
	}
}

func run() {
	var m MediaEngine
	var s SettingEngine
	if len(config.PublicIP) > 0 {
		s.SetNAT1To1IPs(config.PublicIP, ICECandidateTypeHost)
	}
	if config.PortMin > 0 && config.PortMax > 0 {
		s.SetEphemeralUDPPortRange(config.PortMin, config.PortMax)
	}
	// m.RegisterDefaultCodecs()
	webrtc.RegisterCodecs(&m)
	api = NewAPI(WithMediaEngine(&m), WithSettingEngine(s))
	http.HandleFunc("/api/webrtc/play", func(w http.ResponseWriter, r *http.Request) {
		utils.CORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		streamPath := r.URL.Query().Get("streamPath")
		var offer SessionDescription
		var rtc WebRTC
		sub := engine.Subscriber{
			ID:   r.RemoteAddr,
			Type: "WebRTC",
		}
		bytes, err := ioutil.ReadAll(r.Body)
		defer func() {
			if err != nil {
				utils.Println(err)
				fmt.Fprintf(w, `{"errmsg":"%s"}`, err)
				return
			}
		}()
		if err != nil {
			return
		}
		if err = json.Unmarshal(bytes, &offer); err != nil {
			return
		}

		if err = sub.Subscribe(streamPath); err != nil {
			return
		}

		if rtc.PeerConnection, err = api.NewPeerConnection(Configuration{}); err != nil {
			return
		}
		rtc.OnICECandidate(func(ice *ICECandidate) {
			if ice != nil {
				utils.Println(ice.ToJSON().Candidate)
			}
		})
		if err = rtc.SetRemoteDescription(offer); err != nil {
			return
		}
		vt := sub.WaitVideoTrack("h264")
		at := sub.WaitAudioTrack("pcma", "pcmu")
		var videoTrack *TrackLocalStaticSample
		var rtpSender *RTPSender
		if vt != nil {
			pli := "42001f"
			pli = fmt.Sprintf("%x", vt.ExtraData.NALUs[0][1:4])
			if !strings.Contains(offer.SDP, pli) {
				pli = reg_level.FindAllStringSubmatch(offer.SDP, -1)[0][1]
			}
			if videoTrack, err = NewTrackLocalStaticSample(RTPCodecCapability{MimeType: MimeTypeH264, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" + pli}, "video", "m7s"); err != nil {
				return
			}
			rtpSender, err = rtc.AddTrack(videoTrack)
			if err != nil {
				return
			}
			var lastTimeStampV uint32
			sub.OnVideo = func(ts uint32, pack *engine.VideoPack) {
				var s uint32 = 40
				if lastTimeStampV > 0 {
					s = ts - lastTimeStampV
				}
				lastTimeStampV = ts
				if pack.IDR {
					err = videoTrack.WriteSample(media.Sample{
						Data: vt.ExtraData.NALUs[0],
					})
					err = videoTrack.WriteSample(media.Sample{
						Data: vt.ExtraData.NALUs[1],
					})
				}
				for _, nalu := range pack.NALUs {
					err = videoTrack.WriteSample(media.Sample{
						Data:     nalu,
						Duration: time.Millisecond * time.Duration(s),
					})
				}
			}
			go func() {
				rtcpBuf := make([]byte, 1500)
				for {
					if n, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {

						return
					} else {
						if p, err := rtcp.Unmarshal(rtcpBuf[:n]); err == nil {
							for _, pp := range p {
								switch pp.(type) {
								case *rtcp.PictureLossIndication:

									fmt.Println("PictureLossIndication")
								}
							}
						}
					}
				}
			}()
		}
		if at != nil {
			var audioTrack *TrackLocalStaticSample
			audioMimeType := MimeTypePCMA
			if at.CodecID == 8 {
				audioMimeType = MimeTypePCMU
			}
			if audioTrack, err = NewTrackLocalStaticSample(RTPCodecCapability{audioMimeType, 8000, 0, "", nil}, "audio", "m7s"); err != nil {
				return
			}
			if _, err = rtc.AddTrack(audioTrack); err != nil {
				return
			}
			var lastTimeStampA uint32
			sub.OnAudio = func(ts uint32, pack *engine.AudioPack) {
				var s uint32
				if lastTimeStampA > 0 {
					s = ts - lastTimeStampA
				}
				lastTimeStampA = ts
				audioTrack.WriteSample(media.Sample{
					Data: pack.Raw, Duration: time.Millisecond * time.Duration(s),
				})
			}
		}

		if bytes, err := rtc.GetAnswer(); err == nil {
			w.Write(bytes)
			rtc.OnConnectionStateChange(func(pcs PeerConnectionState) {
				utils.Printf("%s Connection State has changed %s ", streamPath, pcs.String())
				switch pcs {
				case PeerConnectionStateConnected:
					if at != nil {
						go sub.PlayAudio(at)
					}
					if vt != nil {
						go sub.PlayVideo(vt)
					}
				case PeerConnectionStateDisconnected, PeerConnectionStateFailed:
					sub.Close()
					rtc.PeerConnection.Close()
				}
			})

		} else {
			return
		}
	})

	http.HandleFunc("/api/webrtc/publish", func(w http.ResponseWriter, r *http.Request) {
		utils.CORS(w, r)
		streamPath := r.URL.Query().Get("streamPath")
		offer := SessionDescription{}
		bytes, err := ioutil.ReadAll(r.Body)
		err = json.Unmarshal(bytes, &offer)
		if err != nil {
			utils.Println(err)
			return
		}
		rtc := new(WebRTC)
		if rtc.PeerConnection, err = api.NewPeerConnection(Configuration{}); err != nil {
			return
		}
		if rtc.Publish(streamPath) {
			if err := rtc.SetRemoteDescription(offer); err != nil {
				utils.Println(err)
				return
			}
			if bytes, err = rtc.GetAnswer(); err == nil {
				w.Write(bytes)
			} else {
				utils.Println("GetAnswer:", err)
				w.Write([]byte(err.Error()))
				return
			}
		} else {
			w.Write([]byte("bad name"))
		}
	})
}
