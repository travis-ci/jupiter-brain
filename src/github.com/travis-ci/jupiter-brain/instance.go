package jupiterbrain

import (
	"time"

	"github.com/lib/pq"
)

// Instance is our representation of an instance woop woop
type Instance struct {
	ID          string `db:"id"`
	IPAddresses []string
	State       string
	CreatedAt   time.Time   `db:"created_at"`
	DestroyedAt pq.NullTime `db:"destroyed_at"`
}
