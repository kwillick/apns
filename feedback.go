package apns

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

var AppleFeedbackProductionGateway = Gateway{
	Host: "feedback.push.apple.com",
	Port: "2196",
}

var AppleFeedbackDevelopmentGateway = Gateway{
	Host: "feedback.sandbox.push.apple.com",
	Port: "2196",
}

// FeedbackResponse represents a device token that Apple has
// indicated should not be sent to in the future.
type FeedbackResponse struct {
	Timestamp   time.Time
	DeviceToken string
}

func FeedbackResponseFromBytes(data []byte) (*FeedbackResponse, error) {
	byteReader := bytes.NewReader(data[0:4])
	var timestamp uint
	if err := binary.Read(byteReader, binary.BigEndian, &timestamp); err != nil {
		return nil, err
	}

	byteReader = bytes.NewReader(data[5:7])
	var length uint16
	if err := binary.Read(byteReader, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	if length != 32 {
		return nil, errors.New(fmt.Sprintf("Expected device token length of 32, got '%v'", length))
	}

	response := &FeedbackResponse{
		Timestamp:   time.Unix((int64)(timestamp), 0),
		DeviceToken: hex.EncodeToString(data[6:]),
	}
	return response, nil
}

// Timestamp + Length + Device Token
const feedbackResponseSizeBytes = 4 + 2 + 32

type FeedbackConnection struct {
	Gateway   string
	tlsConfig tls.Config
	tlsConn   *tls.Conn
}

func NewFeedbackConnection(gateway Gateway, certificateFile string, keyFile string) (*FeedbackConnection, error) {
	cert, certErr := tls.LoadX509KeyPair(certificateFile, keyFile)
	if certErr != nil {
		return nil, certErr
	}

	fc := &FeedbackConnection{
		Gateway: net.JoinHostPort(gateway.Host, gateway.Port),
		tlsConfig: tls.Config{
			Certificates: []tls.Certificate{cert},
			ServerName:   gateway.Host,
		},
	}
	return fc, nil
}

// ListenForFeedback connects to the Apple Feedback Service
// and checks for device tokens.
func (fc *FeedbackConnection) ListenForFeedback(responseChannel chan *FeedbackResponse) error {
	if responseChannel == nil {
		return errors.New("Got a nil response channel")
	}

	var err error
	fc.tlsConn, err = tls.Dial("tcp", fc.Gateway, &fc.tlsConfig)
	if err != nil {
		close(responseChannel)
		return err
	}

	fc.tlsConn.SetReadDeadline(time.Time{})
	buffer := make([]byte, feedbackResponseSizeBytes)
	for {
		_, readErr := fc.tlsConn.Read(buffer)
		if readErr != nil {
			if readErr == io.EOF {
				// No data available. Sleep for 30 minutes before trying again
				fc.tlsConn.Close()
				time.Sleep(time.Duration(30) * time.Minute)

				// Reconnect
				fc.tlsConn, err = tls.Dial("tcp", fc.Gateway, &fc.tlsConfig)
				if err != nil {
					close(responseChannel)
					return err
				}
				continue
			}

			close(responseChannel)
			return readErr
		}

		response, err := FeedbackResponseFromBytes(buffer)
		if err != nil {
			close(responseChannel)
			return err
		}

		responseChannel <- response
	}

	return nil
}
