package apns

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"os"
	"sync"
	"time"
)

type Gateway struct {
	Host string
	Port string
}

var AppleProductionGateway = Gateway{
	Host: "gateway.push.apple.com",
	Port: "2195",
}

var AppleDevelopmentGateway = Gateway{
	Host: "gateway.sandbox.push.apple.com",
	Port: "2195",
}

type PushNotificationError struct {
	Notification *PushNotification
	err          error
}

func (p *PushNotificationError) Error() string {
	return p.err.Error()
}

type Connection struct {
	Gateway             string
	tlsConfig           tls.Config
	tlsConn             *tls.Conn
	running             bool
	idle                bool
	notificationChannel chan *PushNotification
	errorChannel        chan<- *PushNotificationError
	idleTimeoutChannel  chan bool
	activeMutex         sync.Mutex
	activeNotifications map[uint32]*PushNotification
}

func NewConnection(gateway Gateway, certificateFile string, keyFile string) (*Connection, error) {
	c := &Connection{
		Gateway:             net.JoinHostPort(gateway.Host, gateway.Port),
		running:             false,
		idle:                true,
		activeNotifications: map[uint32]*PushNotification{},
	}

	cert, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, err
	}

	c.tlsConfig = tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   gateway.Host,
	}

	return c, nil
}

// This creates a TLS connection that only trusts certificates from the specified root CA
func ConnectionWithRootCA(gateway Gateway, caRootCertFile, certFile, keyFile string) (*Connection, error) {
	c := &Connection{
		Gateway:             net.JoinHostPort(gateway.Host, gateway.Port),
		running:             false,
		idle:                true,
		activeNotifications: map[uint32]*PushNotification{},
	}

	caRootFile, openErr := os.Open(caRootCertFile)
	if openErr != nil {
		return nil, openErr
	}

	caRootBytes, readErr := ioutil.ReadAll(caRootFile)
	if readErr != nil {
		return nil, readErr
	}

	x509Cert, parseErr := x509.ParseCertificate(caRootBytes)
	if parseErr != nil {
		return nil, parseErr
	}

	// Load the root CA file
	rootCA := x509.NewCertPool()
	rootCA.AddCert(x509Cert)

	cert, loadErr := tls.LoadX509KeyPair(certFile, keyFile)
	if loadErr != nil {
		return nil, loadErr
	}

	c.tlsConfig = tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   gateway.Host,
		RootCAs:      rootCA,
	}

	return c, nil
}

func isErrorTimeout(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

func (c *Connection) tcpConnect() error {
	var err error
	c.tlsConn, err = tls.Dial("tcp", c.Gateway, &c.tlsConfig)
	return err
}

func (c *Connection) responseListener() {
	buffer := make([]byte, 6)
	for {
		if !c.running {
			return
		}

		// Set timeout to fire in 5 minutes.
		c.tlsConn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		_, err := c.tlsConn.Read(buffer)
		switch err {
		case nil:
			// Got a response from apple, so the last send failed.
			// The next time read is called it will detect the connection being closed
			// (if it still is) in the io.EOF case
			status := buffer[1]

			byteReader := bytes.NewReader(buffer[2:])
			var identifier uint32
			if err := binary.Read(byteReader, binary.BigEndian, &identifier); err != nil {
				c.Close()
				c.errorChannel <- &PushNotificationError{nil, err}
				return
			}

			c.activeMutex.Lock()
			notification, present := c.activeNotifications[identifier]
			if present {
				delete(c.activeNotifications, identifier)
				c.activeMutex.Unlock()

				// TODO should send respone object that implements Error interface
				err := errors.New(ApplePushResponses[status])
				c.errorChannel <- &PushNotificationError{notification, err}
			} else {
				c.activeMutex.Unlock()
			}

			// Cancel the current timeout
			c.tlsConn.SetReadDeadline(time.Time{})

		case io.EOF:
			// The connection was closed. Apple closes the connection after sending us an error
			// response. So reconnect before going back to reading.
			if c.running {
				connectErr := c.tcpConnect()
				if connectErr != nil {
					// Instead of repeatedly attempting to renew, just send an error and close down
					c.Close()
					c.errorChannel <- &PushNotificationError{nil, connectErr}
					return
				}
			}

		default:
			// Check for a timeout. A timeout is not a failure.
			if !isErrorTimeout(err) {
				// Some other error that isn't recoverable.
				c.Close()
				c.errorChannel <- &PushNotificationError{nil, err}
				return
			}
		}
	}
}

func (c *Connection) send(notification *PushNotification) bool {
	payload, bytesErr := notification.ToBytes()
	if bytesErr != nil {
		// Close down on error
		c.Close()
		c.errorChannel <- &PushNotificationError{notification, bytesErr}
		return false
	}

	c.activeMutex.Lock()
	c.activeNotifications[notification.Identifier] = notification
	c.activeMutex.Unlock()

	_, writeErr := c.tlsConn.Write(payload)
	if writeErr != nil {
		// Close down on error
		c.Close()
		c.errorChannel <- &PushNotificationError{notification, writeErr}
		return false
	}

	c.activeMutex.Lock()
	delete(c.activeNotifications, notification.Identifier)
	c.activeMutex.Unlock()

	return true
}

func (c *Connection) notificationSender() {
	c.notificationChannel = make(chan *PushNotification, 1024)
	idleTimeout := func(channel chan<- bool) {
		time.Sleep(time.Duration(30) * time.Minute)
		channel <- true
	}

	var sentSinceIdleCheck uint32 = 0
	for {
		if !c.running {
			return
		}

		c.idleTimeoutChannel = make(chan bool)
		go idleTimeout(c.idleTimeoutChannel)

		select {
		case notification, ok := <-c.notificationChannel:
			if !ok || !c.running {
				// We were stopped or some other error stopped us
				return
			}

			// Send the data
			if didSend := c.send(notification); didSend {
				sentSinceIdleCheck++
			}

		case <-c.idleTimeoutChannel:
			// Check if we should enter idle state
			if !c.idle && sentSinceIdleCheck == 0 && len(c.notificationChannel) == 0 {
				// Enter idle state and close down connection
				c.idle = true
				c.tlsConn.Close()
				return
			}
		}
	}
}

func (c *Connection) Connect(errorChannel chan<- *PushNotificationError) error {
	if errorChannel == nil {
		return errors.New("Got a nil errors channel")
	}
	if c.running {
		return errors.New("Already running")
	}

	connectErr := c.tcpConnect()
	if connectErr != nil {
		return connectErr
	}

	c.running = true
	c.idle = false
	c.errorChannel = errorChannel

	go c.responseListener()
	go c.notificationSender()

	return nil
}

func (c *Connection) Close() {
	c.running = false
	if c.tlsConn != nil {
		c.tlsConn.Close()
		c.tlsConn = nil
	}
	if c.notificationChannel != nil {
		close(c.notificationChannel)
		c.notificationChannel = nil
	}
	if c.idleTimeoutChannel != nil {
		close(c.idleTimeoutChannel)
		c.idleTimeoutChannel = nil
	}
}

func (c *Connection) SendNotification(deviceToken string, alert string, sound string, badge int, extra map[string]interface{}) {
	go func() {
		notification := NewPushNotification()

		payload := &Payload{
			Alert: alert,
			Sound: sound,
		}
		payload.SetBadge(badge)
		notification.AddPayload(payload)

		for k, v := range extra {
			notification.Set(k, v)
		}
		notification.DeviceToken = deviceToken

		// If we are idle, then the connection needs to be reconnected
		if c.idle {
			if connectErr := c.tcpConnect(); connectErr != nil {
				c.Close()
				c.errorChannel <- &PushNotificationError{nil, connectErr}
				return
			}

		}

		c.notificationChannel <- notification
	}()
}
