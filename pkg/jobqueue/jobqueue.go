package jobqueue

import (
	"time"

	"github.com/go-redis/redis"
	"github.com/keithwachira/go-taskq"
	"k8s.io/klog/v2"
)

func CreateRedisClient(url string) *redis.Client {
	//we start by creating a new go-redis client
	//that we will use to access our redis instance
	rdb := redis.NewClient(&redis.Options{
		Addr:     url,
		Password: "",
		DB:       0, // use default DB
	})

	//make sure we can talk to redis before continuing
	//this probably needs a better failure case
	for i := 0; i < 60; i++ {
		if rdb.Ping().Err() == nil {
			klog.Info("redis responding")
			break
		}
		klog.Info("waiting for redis")
		time.Sleep(time.Second * 2)
	}
	return rdb
}

func StartProcessingQueue(rdb *redis.Client, queueName string, workerLogic func(interface{})) {
	//in this case we have started 1 goroutine so at any moment we will
	//be processing 1 job at a time.
	//you can adjust these parameters to increase or reduce
	q := taskq.NewQueue(1, 1, workerLogic)
	//call startWorkers  in a different goroutine otherwise it will block
	go q.StartWorkers()
	//with our workers running now we can start listening to new events from redis stream
	//we ise a timeout of 0 on BLPop to wait until a message is available
	for {
		result, err := rdb.BLPop(0, queueName).Result()
		if err != nil {
			klog.Errorf("failed poping message: %w", err)
		}
		///we have received the data we should loop it and queue the messages
		//so that our jobs can start processing
		//result[0] is the stream name
		//result[1] is the value
		q.EnqueueJobBlocking(result[1])
	}
}
