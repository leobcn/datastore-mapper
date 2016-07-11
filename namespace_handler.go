package mapper

import (
	"net/http"

	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/taskqueue"
)

const (
	namespaceURL         = "/namespace"
	namespaceCompleteURL = "/namespace/complete"
)

func init() {
	Server.HandleFunc(namespaceURL, namespaceHandler)
	Server.HandleFunc(namespaceCompleteURL, namespaceCompleteHandler)
}

func namespaceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		return
	}

	c := appengine.NewContext(r)

	id, seq, queue, _ := ParseLock(r)
	log.Infof(c, "namespace %s %d", id, seq)

	k := datastore.NewKey(c, config.DatastorePrefix+namespaceKind, id, 0, nil)
	ns := new(namespaceState)

	if err := GetLock(c, k, ns, seq); err != nil {
		log.Errorf(c, "error %s", err.Error())
		if serr, ok := err.(*LockError); ok {
			// for locking errors, the error gives us the response to use
			w.WriteHeader(serr.Response)
			w.Write([]byte(serr.Error()))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
		}
		return
	}

	ns.id = id
	ns.queue = queue

	j, err := getJob(c, ns.jobID())
	if err != nil {
		log.Errorf(c, "error %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		ClearLock(c, k, ns, false)
		return
	}

	if j.Abort {
		w.WriteHeader(http.StatusOK)
		return
	}

	ns.job = j

	err = ns.split(c)
	if err != nil {
		// this will cause a task retry
		log.Errorf(c, "error %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		ClearLock(c, k, ns, false)
		return
	}

	// update namespace status within a transaction
	err = storage.RunInTransaction(c, func(tc context.Context) error {
		fresh := new(namespaceState)
		if err := storage.Get(tc, k, fresh); err != nil {
			return err
		}

		// shards can already be processing ahead of the total being written
		fresh.ShardsTotal = ns.ShardsTotal
		fresh.RequestID = ""

		// if all shards have completed, schedule namespace/completed to update job
		if fresh.ShardsSuccessful == fresh.ShardsTotal {
			t := NewLockTask(k, fresh, config.BasePath+namespaceCompleteURL, nil)
			if _, err := taskqueue.Add(tc, t, queue); err != nil {
				log.Errorf(c, "add task %s", err.Error())
				return err
			}
		}

		if _, err := storage.Put(tc, k, fresh); err != nil {
			return err
		}

		return nil
	}, &datastore.TransactionOptions{XG: true, Attempts: attempts})

	if err != nil {
		log.Errorf(c, "error %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		ClearLock(c, k, ns, false)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func namespaceCompleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		return
	}

	c := appengine.NewContext(r)

	id, seq, queue, _ := ParseLock(r)
	log.Infof(c, "namespace complete %s %d", id, seq)

	k := datastore.NewKey(c, config.DatastorePrefix+namespaceKind, id, 0, nil)
	ns := new(namespaceState)

	if err := GetLock(c, k, ns, seq); err != nil {
		log.Errorf(c, "error %s", err.Error())
		if serr, ok := err.(*LockError); ok {
			// for locking errors, the error gives us the response to use
			w.WriteHeader(serr.Response)
			w.Write([]byte(serr.Error()))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
		}
		return
	}

	ns.id = id
	ns.queue = queue

	j, err := getJob(c, ns.jobID())
	if err != nil {
		log.Errorf(c, "error %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		ClearLock(c, k, ns, false)
		return
	}

	if j.Abort {
		w.WriteHeader(http.StatusOK)
		return
	}

	ns.job = j

	if err := ns.rollup(c); err != nil {
		log.Errorf(c, "error %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		ClearLock(c, k, ns, false)
		return
	}

	ns.complete()
	ns.RequestID = ""

	// update namespace status and job within a transaction
	jk := datastore.NewKey(c, config.DatastorePrefix+jobKind, ns.jobID(), 0, nil)
	err = storage.RunInTransaction(c, func(tc context.Context) error {
		fresh := new(namespaceState)
		keys := []*datastore.Key{k, jk}
		vals := []interface{}{fresh, j}
		if err := storage.GetMulti(tc, keys, vals); err != nil {
			return err
		}

		if j.Abort {
			return nil
		}

		fresh.copyFrom(*ns)
		j.NamespacesSuccessful++
		j.common.rollup(ns.common)

		// TODO: schedule a job completion for symetry and final cleanup, notification etc... ?
		if j.NamespacesSuccessful == j.NamespacesTotal && !j.Iterating {
			j.complete()
			j.RequestID = ""
		}

		if _, err := storage.PutMulti(tc, keys, vals); err != nil {
			return err
		}
		return nil
	}, &datastore.TransactionOptions{XG: true, Attempts: attempts})

	if err != nil {
		log.Errorf(c, "error %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		ClearLock(c, k, ns, false)
		return
	}

	w.WriteHeader(http.StatusOK)
}
