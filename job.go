package mapper

import (
	"github.com/captaincodeman/datastore-locker"
	"golang.org/x/net/context"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
)

type (
	// job is the datastore struct to control execution of a job instance
	job struct {
		locker.Lock
		common

		// TODO: store serialized job spec so it can be used for parameters

		// Job is the job processor name
		JobName string `datastore:"job_name"`

		// Bucket defines the cloud storage bucket to write output to
		// if empty, then no output will be written
		Bucket string `datastore:"bucket,noindex"`

		// Abort is used to cancel a job
		Abort bool `datastore:"abort,noindex"`

		// Shards is the target number of shards to use when splitting a namespace
		Shards int `datastore:"shards,noindex"`

		// Iterating indicates if the iterator is still active
		Iterating bool `datastore:"iterating,noindex"`

		// NamespacesTotal is the total number of namespaces generated by the iterator
		NamespacesTotal int `datastore:"ns_total,noindex"`

		// NamespacesSuccessful is the number of namespaces completed successfully
		NamespacesSuccessful int `datastore:"ns_successful,noindex"`

		// NamespacesFailed is the number of namespaces failed
		NamespacesFailed int `datastore:"ns_failed,noindex"`
	}
)

const (
	jobKind = "job"
)

func (j *job) start(c context.Context, mapper *mapper) error {
	if mapper.config.LogVerbose {
		log.Debugf(c, "creating iterator for job %s", j.id)
	}

	iterator := new(iterator)
	iterator.start(j.Query)

	key := datastore.NewKey(c, mapper.config.DatastorePrefix+iteratorKind, j.id, 0, nil)
	return mapper.locker.Schedule(c, key, iterator, mapper.config.Path+iteratorURL, nil)
}

func (j *job) completed(c context.Context, mapper *mapper, key *datastore.Key) error {
	j.complete()
	j.Lock.Complete()

	// everything is complete when this runs, so no need for a transaction
	if _, err := storage.Put(c, key, j); err != nil {
		return err
	}

	return nil
}

// Load implements the datastore PropertyLoadSaver imterface
func (j *job) Load(props []datastore.Property) error {
	datastore.LoadStruct(j, props)
	j.common.Load(props)

	return nil
}

// Save implements the datastore PropertyLoadSaver imterface
func (j *job) Save() ([]datastore.Property, error) {
	props, err := datastore.SaveStruct(j)
	if err != nil {
		return nil, err
	}

	jprops, err := j.common.Save()
	if err != nil {
		return nil, err
	}
	props = append(props, jprops...)

	return props, nil
}
