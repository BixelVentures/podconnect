package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// roomControlRequest is the one control call for a room: POST /api/room-control.
// Last command wins: there is no prepare step, lease or token. Whatever source
// owns the room (Spotify or radio) gets the command.
type roomControlRequest struct {
	RoomID     string `json:"room_id"`
	Action     string `json:"action"` // pause, resume, next, previous, seek, volume
	PositionMS *int   `json:"position_ms,omitempty"`
	Volume     *int   `json:"volume,omitempty"`
}

type roomControlReply struct {
	OK     bool   `json:"ok"`
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

func roomControlHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var q roomControlRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&q); err != nil {
		roomControlFail(w, http.StatusBadRequest, "", "invalid_request")
		return
	}
	if reason := q.invalid(); reason != "" {
		roomControlFail(w, http.StatusBadRequest, "", reason)
		return
	}
	rooms := loadRooms()
	room := roomByIDIn(rooms, q.RoomID)
	primary := primaryRoom()
	if room == nil || primary == nil {
		roomControlFail(w, http.StatusNotFound, "", "unknown_room")
		return
	}
	if radioOwner.playingOn(room.ID) {
		roomControlRadio(w, r, q)
		return
	}
	gl := librespotStatus(primary.Librespot)
	devs, _ := fetchOutputsFrom(primary.OwnTone)
	if !gl.Active || gl.Stopped || activeRouteRoomID(rooms, devs, gl) != room.ID {
		roomControlFail(w, http.StatusConflict, "", "room_not_playing")
		return
	}
	if err := roomControlSpotify(primary, q); err != nil {
		log.Printf("room-control[%s]: %s failed: %v", room.ID, q.Action, err)
		roomControlFail(w, http.StatusBadGateway, "spotify", "speaker_unavailable")
		return
	}
	writeJSON(w, roomControlReply{OK: true, Source: "spotify"})
}

func (q roomControlRequest) invalid() string {
	switch q.Action {
	case "pause", "resume", "next", "previous":
		if q.PositionMS != nil || q.Volume != nil {
			return "invalid_arguments"
		}
	case "seek":
		if q.PositionMS == nil || *q.PositionMS < 0 || q.Volume != nil {
			return "invalid_arguments"
		}
	case "volume":
		if q.Volume == nil || *q.Volume < 0 || *q.Volume > 100 || q.PositionMS != nil {
			return "invalid_arguments"
		}
	default:
		return "unsupported_action"
	}
	if q.RoomID == "" {
		return "unknown_room"
	}
	return ""
}

// roomControlSpotify sends the command to the one Spotify engine. A pause also
// pauses OwnTone, so the HomePod goes quiet even if the engine is slow.
func roomControlSpotify(primary *Room, q roomControlRequest) error {
	base := primary.Librespot
	switch q.Action {
	case "pause":
		err := librespotTransport(base, "pause")
		if airplayErr := owntoneTransport(primary.OwnTone, "pause"); err == nil {
			err = airplayErr
		}
		return err
	case "resume", "next":
		return librespotTransport(base, q.Action)
	case "previous":
		return librespotTransport(base, "prev")
	case "seek":
		return librespotPostJSON(base, "/player/seek", map[string]int{"position": *q.PositionMS})
	case "volume":
		// The bridge mirrors the engine volume onto the room's HomePod.
		setLibrespotVolumePct(base, *q.Volume)
		return nil
	}
	return fmt.Errorf("unsupported action %q", q.Action)
}

// roomControlRadio maps the room command onto the radio: pause stops the
// station, volume sets it, resume keeps it playing. Tracks do not exist on radio.
func roomControlRadio(w http.ResponseWriter, r *http.Request, q roomControlRequest) {
	command := radioCommand{ActionID: roomControlActionID()}
	switch q.Action {
	case "pause":
		command.Action = "stop"
	case "volume":
		command.Action = "volume"
		command.VolumePercent = q.Volume
	case "resume":
		writeJSON(w, roomControlReply{OK: true, Source: "radio"})
		return
	default:
		roomControlFail(w, http.StatusConflict, "radio", "unsupported_for_radio")
		return
	}
	if _, err := radioOwner.execute(r.Context(), command); err != nil {
		log.Printf("room-control[%s]: radio %s failed: %v", q.RoomID, q.Action, err)
		roomControlFail(w, http.StatusBadGateway, "radio", "speaker_unavailable")
		return
	}
	writeJSON(w, roomControlReply{OK: true, Source: "radio"})
}

func roomControlFail(w http.ResponseWriter, status int, source, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(roomControlReply{Source: source, Error: reason})
}

func roomControlActionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "room-control-" + hex.EncodeToString(b)
}

func librespotPostJSON(base, path string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	cl := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("go-librespot %s returned HTTP %d", path, resp.StatusCode)
	}
	return nil
}
