package jupiterbrain

import "time"

// Instance is our representation of an instance woop woop
type Instance struct {
	ID          string `db:"id"`
	IPAddresses []string
	State       string
	CreatedAt   time.Time `db:"created_at"`
}
