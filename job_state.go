package mapper

import (
	"bytes"

	"encoding/gob"

	"golang.org/x/net/context"
	"google.golang.org/appengine/datastore"
)

type (
	// jobState is the datastore struct to control execution of a job instance
	jobState struct {
		common
		lock

		// Job is the job processor struct
		Job Job `datastore:"-"`

		// Query provides the source to process
		Query *Query `datastore:"-"`

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

func getJob(c context.Context, id string) (*jobState, error) {
	key := datastore.NewKey(c, config.DatastorePrefix+jobKind, id, 0, nil)
	job := new(jobState)
	if err := storage.Get(c, key, job); err != nil {
		return nil, err
	}
	return job, nil
}

func (j *jobState) start(c context.Context) error {
	iterator := new(iterator)
	iterator.start()

	k := datastore.NewKey(c, config.DatastorePrefix+iteratorKind, j.id, 0, nil)
	if _, err := ScheduleLock(c, k, iterator, config.BasePath+iteratorURL, nil, j.queue); err != nil {
		return err
	}
	return nil
}

/* datastore */
func (j *jobState) Load(props []datastore.Property) error {
	datastore.LoadStruct(j, props)

	for _, prop := range props {
		switch prop.Name {
		case "job":
			payload := bytes.NewBuffer(prop.Value.([]byte))
			enc := gob.NewDecoder(payload)
			if err := enc.Decode(&j.Job); err != nil {
				return err
			}
		case "query":
			j.Query = &Query{}
			if err := j.Query.GobDecode(prop.Value.([]byte)); err != nil {
				return err
			}
		}
	}

	j.common.Load(props)
	j.lock.Load(props)

	return nil
}

func (j *jobState) Save() ([]datastore.Property, error) {
	props, err := datastore.SaveStruct(j)
	if err != nil {
		return nil, err
	}

	payload := new(bytes.Buffer)
	enc := gob.NewEncoder(payload)
	if err := enc.Encode(&j.Job); err != nil {
		return nil, err
	}
	props = append(props, datastore.Property{Name: "job", Value: payload.Bytes(), NoIndex: true, Multiple: false})

	b, err := j.Query.GobEncode()
	if err != nil {
		return nil, err
	}
	props = append(props, datastore.Property{Name: "query", Value: b, NoIndex: true, Multiple: false})

	jprops, err := j.common.Save()
	if err != nil {
		return nil, err
	}
	props = append(props, jprops...)

	lprops, err := j.lock.Save()
	if err != nil {
		return nil, err
	}
	props = append(props, lprops...)

	return props, nil
}
