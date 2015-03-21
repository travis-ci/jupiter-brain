package jupiterbrain

type Instance struct {
	ID          string   `json:"id"`
	IPAddresses []string `json:"ip_addresses"`
	State       string   `json:"state"`
}
