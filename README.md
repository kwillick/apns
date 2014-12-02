# apns

This a fork of [anachronistic/apns](https://github.com/anachronistic/apns) that adds a persistent
connection to the specified gateway and non-blocking sending of notifications.

## Usage

### Connecting to a gateway and sending a notification
```go
package main

import (
  "fmt"
  apns "github.com/kwillick/apns"
  "time"
)

func main() {
  connection, connErr := apns.NewConnection(apns.AppleDevelopmentGateway, "YOUR_CERT_FILE.pem", "YOUR_KEY_FILE.pem")
  if connErr != nil {
      connection.Close()
      panic(connErr)
  }

  errorChannel := make(chan *apns.PushNotificationError)
  err = connection.Connect(errorChannel)
  if err != nil {
      conn.Close()
	  panic(err)
  }

  // Listen for errors and panic if an error occurs
  go func() {
      err := <-errorChannel
      panic(err)
  }()

  fakeDeviceToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  connection.SendNotification(fakeDeviceToken, "Some alert text", "bingbong.aiff", 1, nil)

  // Sleep, since SendNotification doesn't block
  time.Sleep(time.Duration(2) * time.Second)
}
```

### Checking the feedback service
```go
package main

import (
  "fmt"
  apns "github.com/kwillick/apns"
  "os"
)

func main() {
  fmt.Println("- connecting to check for deactivated tokens (maximum read timeout =", apns.FeedbackTimeoutSeconds, "seconds)")

  client := apns.NewClient("feedback.sandbox.push.apple.com:2196", "YOUR_CERT_PEM", "YOUR_KEY_NOENC_PEM")
  go client.ListenForFeedback()

  for {
    select {
    case resp := <-apns.FeedbackChannel:
      fmt.Println("- recv'd:", resp.DeviceToken)
    case <-apns.ShutdownChannel:
      fmt.Println("- nothing returned from the feedback service")
      os.Exit(1)
    }
  }
}
```

#### Returns
```shell
- connecting to check for deactivated tokens (maximum read timeout = 5 seconds)
- nothing returned from the feedback service
exit status 1
```

Your output will differ if the service returns device tokens.

```shell
- recv'd: DEVICE_TOKEN_HERE
...etc.
```
