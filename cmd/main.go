/*
Copyright 2018 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/go-redis/redis"
	"github.com/google/uuid"
	"github.com/keithwachira/go-taskq"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
)

const (
	streamName = "bot_jobs"
)

func main() {
	klog.InitFlags(nil)

	//we start by creating a new go-redis client
	//that we will use to access our redis instance
	rdb := redis.NewClient(&redis.Options{
		Addr:     "redis-master:6379",
		Password: "",
		DB:       0, // use default DB
	})

	for i := 0; i < 60; i++ {
		if rdb.Ping().Err() == nil {
			klog.Info("redis responding")
			break
		}
		klog.Info("waiting for redis")
		time.Sleep(time.Second * 2)
	}

	//start processing any received job in redis
	//you can see the definition of this function below
	go StartProcessingEmails(rdb)

	leaseLockName := "bot"
	leaseLockNamespace := "default"
	id := uuid.New().String()

	// leader election uses the Kubernetes API by writing to a
	// lock object, which can be a LeaseLock object (preferred),
	// a ConfigMap, or an Endpoints (deprecated) object.
	// Conflicting writes are detected and each client handles those actions
	// independently.
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal(err)
	}
	client := clientset.NewForConfigOrDie(config)

	run := func(ctx context.Context) {
		// complete your controller loop here
		klog.Info("Controller loop...")

		klog.Info("Sleeping for 2 minutes before populating queue...")
		time.Sleep(time.Minute * 2)
		klog.Info("Creating 20 jobs...")
		for i := 1; i <= 20; i++ {
			if err := rdb.RPush(streamName, strconv.Itoa(i)).Err(); err != nil {
				klog.Errorf("could not add data to queue: %w", err)
			}

			klog.Infof("%d added to queue", i)
		}
	}

	// use a Go context so we can tell the leaderelection code when we
	// want to step down
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// listen for interrupts or the Linux SIGTERM signal and cancel
	// our context, which the leader election code will observe and
	// step down
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		klog.Info("Received termination, signaling shutdown")
		cancel()
	}()

	// we use the Lease lock type since edits to Leases are less common
	// and fewer objects in the cluster watch "all Leases".
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseLockName,
			Namespace: leaseLockNamespace,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}

	// start the leader election code loop
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: lock,
		// IMPORTANT: you MUST ensure that any code you have that
		// is protected by the lease must terminate **before**
		// you call cancel. Otherwise, you could have a background
		// loop still running and another process could
		// get elected before your background loop finished, violating
		// the stated goal of the lease.
		ReleaseOnCancel: true,
		LeaseDuration:   60 * time.Second,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// we're notified when we start - this is where you would
				// usually put your code
				run(ctx)
			},
			OnStoppedLeading: func() {
				// we can do cleanup here
				klog.Infof("leader lost: %s", id)
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				// we're notified when new leader elected
				if identity == id {
					// I just got the lock
					return
				}
				klog.Infof("new leader elected: %s", identity)
			},
		},
	})
}

// RedisStreamsProcessing
//you can also pass a database here too
//if you need it to process you work
type RedisStreamsProcessing struct {
	Redis *redis.Client
	//other dependencies e.g. logger database goes here
}

// Process this method implements JobCallBack
///it will read and process each id
func (r *RedisStreamsProcessing) Process(job interface{}) {
	id := job.(string)
	klog.Infof("Processing ID %s", id)

	// peg CPUs for 2min to test scaling
	done := make(chan int)

	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
				}
			}
		}()
	}

	time.Sleep(time.Minute * 2)
	close(done)

	// If we had a problem, we can add the message back to the queue
	/*
		r.Redis.XAdd(context.Background(), &redis.XAddArgs{
				Stream: streamName,
				MaxLen: 0,
				ID: "",
				Values: data.Values,
			})
	*/
}

func StartProcessingEmails(rdb *redis.Client) {
	//create a new consumer instance to process the job
	//and pass it to our task queue
	redisStreams := RedisStreamsProcessing{
		Redis: rdb,
	}
	//in this case we have started 5 goroutines so at any moment we will
	//be sending a maximum of 5 emails.
	//you can adjust these parameters to increase or reduce
	q := taskq.NewQueue(1, 1, redisStreams.Process)
	//call startWorkers  in a different goroutine otherwise it will block
	go q.StartWorkers()
	//with our workers running now we can start listening to new events from redis stream
	//we start from id 0 i.e. the first item in the stream
	for {
		result, err := rdb.BLPop(0, streamName).Result()
		if err != nil {
			klog.Errorf("failed poping message: %w", err)
		}
		///we have received the data we should loop it and queue the messages
		//so that our jobs can start processing
		q.EnqueueJobBlocking(result[1])
	}
}
