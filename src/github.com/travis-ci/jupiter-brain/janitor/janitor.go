package janitor

import (
	"github.com/Sirupsen/logrus"
	"github.com/travis-ci/jupiter-brain"
)

type Janitor struct {
	im  jupiterbrain.InstanceManager
	log *logrus.Logger

	ic chan string
}

func New(im jupiterbrain.InstanceManager, log *logrus.Logger) *Janitor {
	return &Janitor{
		im:  im,
		log: log,

		ic: make(chan string),
	}
}

func (j *Janitor) Run() {
	j.log.Debug("running janitor")
	j.trackInstancesForever()
}

func (j *Janitor) trackInstancesForever() {
	for id := range j.ic {
		j.log.WithFields(logrus.Fields{
			"instance": id,
		}).Debug("tracking")

		// TODO: tracking wat
	}
}

func (j *Janitor) Track(id string) {
	j.ic <- id
}
