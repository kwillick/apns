package apns

// The maximum number of seconds we're willing to wait for a response
// from the Apple Push Notification Service.
const TimeoutSeconds = 5

// This enumerates the response codes that Apple defines
// for push notification attempts.
var ApplePushResponses = map[uint8]string{
	0:   "No errors",
	1:   "Processing error",
	2:   "Missing device token",
	3:   "Missing topic",
	4:   "Missing payload",
	5:   "Invalid token size",
	6:   "Invalid topic size",
	7:   "Invalid payload size",
	8:   "Invalid token",
	10:  "Shutdown",
	255: "None (unknown)",
}

// PushNotificationResponse details what Apple had to say, if anything.
type PushNotificationResponse struct {
	Success       bool
	AppleResponse string
	Error         error
}

// NewPushNotificationResponse creates and returns a new PushNotificationResponse
// structure; it defaults to being unsuccessful at first.
func NewPushNotificationResponse() (resp *PushNotificationResponse) {
	resp = new(PushNotificationResponse)
	resp.Success = false
	return
}
