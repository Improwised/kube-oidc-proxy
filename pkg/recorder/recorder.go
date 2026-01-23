package recorder

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

	authuser "k8s.io/apiserver/pkg/authentication/user"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"

	"github.com/Improwised/kube-oidc-proxy/pkg/storage"
	"github.com/google/uuid"
	"github.com/gvcgo/asciinema/asciicast"
	"github.com/olivere/ndjson"
	"k8s.io/klog/v2"
)

// WebSocket constants for parsing `exec` streams.
const (
	// WebSocketBinaryFrame defines the opcode for a binary frame.
	// Terminal streams are transmitted as binary frames.
	WebSocketBinaryFrame = 130 // 0x82
	// WebSocketPayload16Bit indicates that the payload length is described by the next 2 bytes.
	WebSocketPayload16Bit = 126
	// WebSocketPayload64Bit indicates that the payload length is described by the next 8 bytes.
	WebSocketPayload64Bit = 127
)

// Channel types for terminal streams to distinguish between stdout and stderr.
const (
	ChannelStdout = 1
	ChannelStderr = 2
)

// recordingRoundTripper is a http.RoundTripper that wraps an existing RoundTripper
// to intercept and record terminal sessions from `exec` request.
type RecordingRoundTripper struct {
	OriginalRT http.RoundTripper
	S3Uploader *storage.S3Uploader
}

// RoundTrip intercepts the HTTP round trip. If it detects a WebSocket upgrade,
// it injects the session recording logic.
func (rrt *RecordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := rrt.OriginalRT.RoundTrip(req)

	// A status code of 101 Switching Protocols indicates a WebSocket upgrade,
	// which is used for `exec` sessions. We start recording here.
	if err == nil && res.StatusCode == http.StatusSwitchingProtocols {
		// Check if the request is an `exec` session by looking for the "command" query parameter.
		if len(req.URL.Query()["command"]) == 0 {
			klog.V(5).Infof("Skipping session recording for non-exec WebSocket upgrade: %s", req.URL.Path)
			return res, err // Not an exec session, so don't record.
		}

		recorder, recorderErr := rrt.createSessionRecorder(req, res)
		if recorderErr != nil {
			klog.Errorf("Failed to create session recorder: %v", recorderErr)
			// Proceed without recording on error.
			return res, nil
		}

		if recorder != nil {
			// Replace the original response body with our recorder to intercept the data stream.
			res.Body = recorder
		}
	}
	return res, err
}

// createSessionRecorder prepares and creates a new session recorder.
// It extracts user and command details, sets up a temporary file,
// and wraps the connection in our sessionRecorder.
func (rrt *RecordingRoundTripper) createSessionRecorder(req *http.Request, res *http.Response) (io.ReadWriteCloser, error) {
	user, ok := genericapirequest.UserFrom(req.Context())
	if !ok {
		klog.Warning("Cannot get user from request context for session recording")
		user = &authuser.DefaultInfo{Name: "unknown"}
	}

	// The command being executed is passed as a URL query parameter.
	command := strings.Join(req.URL.Query()["command"], " ")

	// A temporary file is created to store the recording locally before potential upload.
	sessionID := uuid.New().String()
	recordingPath := fmt.Sprintf("/tmp/exec-sessions/%s", user.GetName())
	if err := os.MkdirAll(recordingPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create recording directory: %w", err)
	}

	// The response body for a WebSocket upgrade is a ReadWriteCloser representing the connection.
	backendConn, ok := res.Body.(io.ReadWriteCloser)
	if !ok {
		return nil, fmt.Errorf("underlying response body is not an io.ReadWriteCloser, cannot record")
	}

	// Create and return the session recorder.
	castFilePath := fmt.Sprintf("%s/%s.cast", recordingPath, sessionID)
	recorder := &sessionRecorder{
		backendConn:     backendConn,
		castFile:        castFilePath,
		asciicastStream: asciicast.NewStream(2.0),
		command:         command,
		s3Uploader:      rrt.S3Uploader,
		user:            user,
		sessionID:       sessionID,
		buffer:          bytes.NewBuffer(nil),
	}

	return recorder, nil
}

// sessionRecorder is an io.ReadWriteCloser that records a terminal session.
// It reads from the backend (stdout/stderr), parses WebSocket frames,
// and writes them into an asciicast file. It writes user input back to the backend.
type sessionRecorder struct {
	backendConn     io.ReadWriteCloser
	castFile        string
	asciicastStream *asciicast.Stream
	command         string
	s3Uploader      *storage.S3Uploader
	user            authuser.Info
	sessionID       string
	buffer          *bytes.Buffer
}

// Read intercepts data read from the backend connection (terminal output).
// The data is buffered and processed to record the session.
func (sr *sessionRecorder) Read(p []byte) (int, error) {
	// Read from the actual backend connection.
	n, err := sr.backendConn.Read(p)
	if n > 0 {
		// Buffer the raw data for processing.
		sr.buffer.Write(p[:n])
		// Process the buffer to parse WebSocket frames.
		sr.processFrames()
	}
	return n, err
}

// Close is called when the session terminates. It finalizes the recording
// and closes the underlying connection.
func (sr *sessionRecorder) Close() error {
	// Finalize the recording file and upload it if configured.
	sr.finalizeRecording()
	// Close the actual backend connection.
	return sr.backendConn.Close()
}

// Write passes user input from their terminal to the backend process.
func (sr *sessionRecorder) Write(p []byte) (int, error) {
	return sr.backendConn.Write(p)
}

// processFrames parses WebSocket frames from the internal buffer.
// It handles frame fragmentation and extracts the payload for recording.
func (sr *sessionRecorder) processFrames() {
	for {
		// A WebSocket frame has at least a 2-byte header.
		if sr.buffer.Len() < 2 {
			break // Not enough data for a header, wait for more.
		}

		frameBytes := sr.buffer.Bytes()

		// The terminal stream is sent as binary frames. We skip any other data
		// that might be in the stream (e.g., control frames).
		if frameBytes[0] != WebSocketBinaryFrame {
			nextFrameStart := bytes.IndexByte(frameBytes, WebSocketBinaryFrame)
			if nextFrameStart == -1 {
				sr.buffer.Reset() // No binary frame found, clear buffer.
				break
			}
			sr.buffer.Next(nextFrameStart) // Skip to the next binary frame.
			continue
		}

		// Parse the frame to get the payload and its length.
		payload, frameLength, err := sr.parseWebSocketFrame(frameBytes)
		if err != nil {
			klog.Errorf("Failed to parse WebSocket frame: %v", err)
			sr.buffer.Reset() // On error, reset the buffer to recover.
			break
		}

		if frameLength == 0 {
			// Frame is incomplete, wait for more data.
			break
		}

		// Remove the processed frame from the buffer.
		sr.buffer.Next(frameLength)

		// Record the payload if it's from stdout/stderr.
		if len(payload) > 0 {
			sr.recordPayload(payload)
		}
	}
}

// parseWebSocketFrame decodes a single WebSocket binary frame.
// It returns the application payload, the total frame size, and any errors.
// This function handles 7-bit and 16-bit payload lengths.
func (sr *sessionRecorder) parseWebSocketFrame(data []byte) ([]byte, int, error) {

	if len(data) < 2 {
		return nil, 0, nil // Not enough data for a header.
	}

	// The second byte (mask bit cleared) indicates payload length.
	payloadLength := int(data[1])
	headerSize := 2

	switch payloadLength {
	case WebSocketPayload16Bit:
		// If length is 126, the next 2 bytes are the real length.
		if len(data) < 4 {
			return nil, 0, nil // Not enough data for 16-bit length.
		}
		payloadLength = int(binary.BigEndian.Uint16(data[2:4]))
		headerSize = 4
	case WebSocketPayload64Bit:
		// 64-bit length is not expected from `exec`.
		return nil, 0, fmt.Errorf("64-bit WebSocket frame length not supported")
	}

	// Ensure the full frame is in the buffer.
	frameLength := headerSize + payloadLength
	if len(data) < frameLength {
		return nil, 0, nil // Incomplete frame.
	}

	// The actual data sent by the server.
	frameData := data[headerSize:frameLength]
	if len(frameData) == 0 {
		return nil, frameLength, nil // Valid frame with empty payload.
	}

	// The first byte of the payload identifies the channel (stdout/stderr).
	channel := frameData[0]
	payload := frameData[1:]

	// We only record output channels.
	recordedPayload := payload
	if len(payload) > 0 && (channel == ChannelStdout || channel == ChannelStderr) {
		recordedPayload = payload
	}

	return recordedPayload, frameLength, nil
}

// recordPayload writes the captured output to the asciicast stream.
func (sr *sessionRecorder) recordPayload(payload []byte) {
	if _, err := sr.asciicastStream.Write(payload); err != nil {
		klog.Errorf("Failed to write to asciicast stream: %v", err)
	}
}

// finalizeRecording completes the recording process by generating the final asciicast
// file and uploading it.
func (sr *sessionRecorder) finalizeRecording() {
	// Close the stream to finalize duration and other metadata.
	sr.asciicastStream.Close()

	// Generate the file content in NDJSON format.
	recordingData := sr.generateRecordingData()

	// Write to a local temporary file.
	if err := os.WriteFile(sr.castFile, recordingData, 0644); err != nil {
		klog.Errorf("Failed to write recording file: %v", err)
		return
	}

	// Upload to S3 if it's configured.
	if sr.s3Uploader != nil {
		sr.uploadToS3()
	}
}

// generateRecordingData creates the full content of the .cast file.
// It includes a header with metadata and a sequence of events, formatted as NDJSON.
func (sr *sessionRecorder) generateRecordingData() []byte {
	var buf bytes.Buffer
	writer := ndjson.NewWriter(&buf)

	// Try to determine the shell from the executed command.
	shell := ""
	if len(sr.command) > 0 {
		parts := strings.Fields(sr.command)
		if len(parts) > 0 {
			shell = parts[0]
		}
	}

	// The header contains metadata for the session.
	header := asciicast.Header{
		Version:   2,
		Width:     120, // Default width
		Height:    30,  // Default height
		Timestamp: time.Now().Unix(),
		Duration:  asciicast.Duration(sr.asciicastStream.Duration().Seconds()),
		Command:   sr.command,
		Env: &asciicast.Env{
			Term:  "xterm-256color",
			Shell: shell,
		},
	}

	// Encode header to NDJSON format.
	encoder := json.NewEncoder(&buf)
	if err := encoder.Encode(&header); err != nil {
		klog.Errorf("Failed to encode header: %v", err)
	}

	// Encode each frame (event) to NDJSON format.
	// Frame format: [time, type, data]
	for _, frame := range sr.asciicastStream.Frames {
		frameData := []interface{}{frame.Time, "o", string(frame.EventData)}
		if err := writer.Encode(frameData); err != nil {
			klog.Warningf("Failed to encode frame: %v", err)
		}
	}

	return buf.Bytes()
}

// uploadToS3 handles the process of uploading the recording file to S3.
func (sr *sessionRecorder) uploadToS3() {

	objectKey := fmt.Sprintf("%s/%s.cast", sr.user.GetName(), sr.sessionID)
	if err := sr.s3Uploader.Upload(sr.castFile, objectKey); err != nil {
		klog.Errorf("Failed to upload session recording to S3: %v", err)
		return
	}

	// Clean up local file after a successful upload to save space.
	if err := os.Remove(sr.castFile); err != nil {
		klog.Warningf("Failed to remove temporary recording file: %v", err)
	}
}
