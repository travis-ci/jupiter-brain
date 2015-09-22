package server

import (
	"time"

	"github.com/travis-ci/jupiter-brain"
)

type instanceResponse struct {
	Type        string    `json:"type"`
	ID          string    `json:"id"`
	IPAddresses []string  `json:"ip-addresses"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
	Age         string    `json:"age"`
}

var stateMap = map[string]string{
	"poweredOn":  "powered-on",
	"poweredOff": "powered-off",
	"suspended":  "suspended",
	"":           "",
}

// MarshalInstance takes an instance and marshals it into a JSON-encodable
// interface{} value
func MarshalInstance(instance *jupiterbrain.Instance) interface{} {
	now := time.Now().UTC()

	jsonState, ok := stateMap[instance.State]
	if !ok {
		jsonState = "unknown"
	}

	return instanceResponse{
		Type:        "instances",
		ID:          instance.ID,
		IPAddresses: instance.IPAddresses,
		State:       jsonState,
		CreatedAt:   instance.CreatedAt,
		Age:         now.Sub(instance.CreatedAt).String(),
	}
}
