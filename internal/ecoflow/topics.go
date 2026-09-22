package ecoflow

import "fmt"

// Topics names the MQTT topics of one device on the app channel.
//
// Wildcards are left out deliberately: the broker's access rules refuse them
// while granting the exact topics, so a subscription to "#" produces a false
// negative - exactly the mistake that makes people conclude the channel is
// closed when it is open.
type Topics struct {
	// Push carries the telemetry, as protobuf. This is where the readings are.
	Push string

	// State carries the device's own online and offline announcements.
	State string

	// Reply is where an answer to a request would arrive. Nothing was ever
	// seen on it at this device - the push runs regardless.
	Reply string

	// Get takes read requests. Measured at the device: they are not needed,
	// the subscription alone keeps the data coming.
	Get string

	// Set takes commands that change the device. Only the stream switch is
	// ever published here, and only on an explicit request.
	Set string
}

// TopicsFor names the topics of one device.
func TopicsFor(userID, serial string) Topics {
	thing := fmt.Sprintf("/app/%s/%s/thing/property", userID, serial)
	return Topics{
		Push:  "/app/device/property/" + serial,
		State: "/app/device/status/" + serial,
		Reply: thing + "/get_reply",
		Get:   thing + "/get",
		Set:   thing + "/set",
	}
}

// Subscribe lists the topics worth subscribing to, in that order.
func (t Topics) Subscribe() []string {
	return []string{t.Push, t.Reply, t.State}
}
