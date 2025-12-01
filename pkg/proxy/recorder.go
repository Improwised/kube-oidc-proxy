package proxy

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gvcgo/asciinema/asciicast"
	"github.com/olivere/ndjson"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"
)

// recordingRoundTripper wraps a standard http.RoundTripper to intercept and
// record streaming connections (like exec).
type recordingRoundTripper struct {
	originalRT http.RoundTripper
}

func (rrt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {

	// Execute the request with the original transport
	res, err := rrt.originalRT.RoundTrip(req)

	if err == nil && res.StatusCode == http.StatusSwitchingProtocols {
		user, ok := genericapirequest.UserFrom(req.Context())
		if !ok {
			klog.Warningf("cannot get user from request context for session recording")
			user = &authuser.DefaultInfo{Name: "unknown"}
		}
		sessionID := uuid.New().String()
		recordingPath := fmt.Sprintf("/tmp/exec-sessions/%s", user.GetName())
		if err := os.MkdirAll(recordingPath, 0755); err != nil {
			klog.Errorf("Failed to create recording directory: %v", err)
			return res, nil // Return original response without recorder
		}

		// Create the .cast file
		castFile := fmt.Sprintf("%s/%s.cast", recordingPath, sessionID)

		backendConn, ok := res.Body.(io.ReadWriteCloser)
		if !ok {
			klog.Errorf("Underlying response body is not an io.ReadWriteCloser, cannot record session.")
			return res, nil
		}

		command := strings.Join(req.URL.Query()["command"], " ")

		// Wrap the backend connection in our recorder
		res.Body = &readWriteCloserRecorder{
			ReadWriteCloser: backendConn,
			castFile:        castFile,
			asciicastStream: asciicast.NewStream(2.0),
			command:         command,
		}
	}

	return res, err
}

// readWriteCloserRecorder is a wrapper around the backend connection (an io.ReadWriteCloser)
// that tees the data to a recording file in asciicast format
type readWriteCloserRecorder struct {
	io.ReadWriteCloser
	castFile        string
	asciicastStream *asciicast.Stream
	command         string
}

func (r *readWriteCloserRecorder) Read(p []byte) (int, error) {

	n, err := r.ReadWriteCloser.Read(p)
	if n > 0 {
		data := p[:n]

		// The Kube exec protocol is layered on top of WebSocket frames.
		// We need to parse the WebSocket frame to get to the underlying protocol.
		// Frame format: [opcode (130/0x82), length-info, payload...]
		// Payload format: [channel (1/2), data...]
		if len(data) < 2 || data[0] != 130 { // 130 = 0x82 = binary websocket frame
			klog.Warningf("Unexpected stream data: not a binary websocket frame. Data: %v", data)
			// Pass through without recording, as we don't know the format.
			return n, err
		}

		payloadOffset := 2 // after [opcode, len]
		payloadLen := int(data[1])

		switch {
		case payloadLen == 126:
			// 16-bit length field
			if len(data) < 4 {
				klog.Warningf("Partial frame, not enough data for 16-bit length. Data: %v", data)
				return n, err
			}
			payloadLen = int(binary.BigEndian.Uint16(data[2:4]))
			payloadOffset = 4
		case payloadLen == 127:
			// 64-bit length, not expected for this use case.
			klog.Errorf("Unsupported frame with 64-bit length. Data: %v", data)
			return n, err
		default:
			// Standard 7-bit length (0-125)
			// payloadLen is already correctly set
		}

		// The actual data payload starts after the WS framing header
		framePayload := data[payloadOffset:]

		// Verify that we have the expected amount of payload data
		if len(framePayload) != payloadLen {
			klog.Warningf("Frame payload length mismatch. Expected %d, got %d. Data: %v", payloadLen, len(framePayload), data)
			return n, err
		}

		if len(framePayload) == 0 {
			// Empty payload, nothing to record.
			return n, err
		}

		// Inside the websocket payload, we have the Kube protocol: [channel, data...]
		channel := framePayload[0]
		recorderPayload := framePayload[1:]

		klog.V(5).Infof("Read WS frame: channel=%d, recorder_payload_len=%d", channel, len(recorderPayload))

		// Only record data from stdin (channel 0) and stdout/stderr (channels 1/2)
		if channel == 1 || channel == 2 {
			if _, writeErr := r.asciicastStream.Write(recorderPayload); writeErr != nil {
				klog.Errorf("failed to write to asciicast stream: %v", writeErr)
			}
		}
	}

	return n, err
}

func (r *readWriteCloserRecorder) Close() error {
	err := r.ReadWriteCloser.Close()

	// Finalize asciicast stream
	r.asciicastStream.Close()

	var buf bytes.Buffer

	result := ndjson.NewWriter(&buf)

	shell := ""
	if len(r.command) > 0 {
		shell = strings.Split(r.command, " ")[0]
	}

	header := asciicast.Header{
		Version:   2,
		Width:     120, // Default width, could be made configurable
		Height:    30,  // Default height, could be made configurable
		Timestamp: time.Now().Unix(),
		Duration:  asciicast.Duration(r.asciicastStream.Duration().Seconds()),
		Command:   r.command,
		Env: &asciicast.Env{
			Term:  "xterm-256color",
			Shell: shell,
		},
	}

	enc := json.NewEncoder(&buf)
	if jsonErr := enc.Encode(&header); jsonErr != nil {
		klog.Errorf("Failed to encode header: %v", jsonErr)
	}

	for _, f := range r.asciicastStream.Frames {
		if jsonErr := result.Encode([]interface{}{f.Time, "o", string(f.EventData)}); jsonErr != nil {
			klog.Warningf("Failed to encode frame: %v, error: %v", f, jsonErr)
		}
	}

	if writeErr := os.WriteFile(r.castFile, buf.Bytes(), os.ModePerm); writeErr != nil {
		klog.Warningf("Failed to write frames: %v", writeErr)
	}

	return err
}
