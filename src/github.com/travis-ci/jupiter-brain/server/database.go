package server

import (
	"time"

	// Register PostgreSQL driver bits
	_ "github.com/lib/pq"

	"github.com/jmoiron/sqlx"
	"github.com/travis-ci/jupiter-brain"
)

type database interface {
	SaveInstance(*jupiterbrain.Instance) error
	DestroyInstance(string) error
	FetchInstances(*databaseQuery) ([]*jupiterbrain.Instance, error)
}

type databaseQuery struct {
	MinAge time.Duration
}

type pgDatabase struct {
	conn *sqlx.DB
}

func newPGDatabase(url string) (*pgDatabase, error) {
	conn, err := sqlx.Open("postgres", url)
	if err != nil {
		return nil, err
	}

	return &pgDatabase{
		conn: conn,
	}, nil
}

func (db *pgDatabase) SaveInstance(inst *jupiterbrain.Instance) error {
	_, err := db.conn.Exec(`INSERT INTO jupiter_brain.instances(id, created_at) VALUES ($1, $2)`, inst.ID, inst.CreatedAt)
	return err
}

func (db *pgDatabase) FetchInstances(q *databaseQuery) ([]*jupiterbrain.Instance, error) {
	instances := []*jupiterbrain.Instance{}
	rows, err := db.conn.Queryx(`SELECT * FROM jupiter_brain.instances WHERE destroyed_at IS NULL AND ((now() AT TIME ZONE 'UTC') - created_at) >= $1::interval`, q.MinAge.String())
	if err != nil {
		return instances, err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		instance := &jupiterbrain.Instance{}
		err = rows.StructScan(instance)
		if err != nil {
			return instances, err
		}

		instances = append(instances, instance)
	}

	return instances, nil
}

func (db *pgDatabase) DestroyInstance(id string) error {
	_, err := db.conn.Exec(`UPDATE jupiter_brain.instances SET destroyed_at = now() WHERE id = $1`, id)
	return err
}
