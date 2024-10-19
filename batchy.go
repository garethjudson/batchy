package batchy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// MicroBatch encapsulates the methods for Starting and Shutting down the batch processing
// Job and JobResult generics are used to allow input or output of any types
type MicroBatch[Job interface{}, JobResult interface{}] struct {
	Configuration           MicroBatchConfig[Job, JobResult]
	shutDownChannel         chan bool
	shutDownCompleteChannel chan bool
	timerFunc               func(duration time.Duration) *time.Timer
}

// MicroBatchConfig contains setup configuration for a MicroBatch
// Size The maximum size of batch, once reached the batch is sent to the processor
// Frequency how often to send a micro batch to the processor
// BatchProcessor a processor that will do some work on the inbound Jobs, returning a JobResult
// InChannel this is where Jobs should be sent, these will be batched and sent to the BatchProcessor as Frequency is reached
type MicroBatchConfig[Job interface{}, JobResult interface{}] struct {
	Size            int
	Frequency       *time.Duration
	BatchProcessor  BatchProcessor[Job, JobResult]
	ShutdownTimeout *time.Duration // will default to 30 seconds if not provided
}

// BatchProcessor used by the MicroBatch to process an individual job and returns a job result
type BatchProcessor[Job interface{}, JobResult interface{}] interface {
	// Process the function used to process a Job (interface {}) and return a JobResult (interface {}) and an ok bool flag to return if this function was cancelled
	// ctx is used so that the caller can conveniently use any appropriate context in the implemented Process function
	// This is the context that was passed into Start function
	// Monitor ctx.Done so go routines will get cleaned up, and return ok == false if job was not finished
	Process(ctx context.Context, job []Job) (jobResults []JobResult, ok bool)
}

// NewMicroBatch constructs a new MicroBatch given the appropriate arguments
func NewMicroBatch[Job interface{}, JobResult interface{}](configuration MicroBatchConfig[Job, JobResult]) (*MicroBatch[Job, JobResult], error) {
	// could replace with sensible defaults
	if configuration.Size < 1 {
		return nil, errors.New("configuration.Size must be greater than zero")
	}
	if configuration.Frequency == nil {
		return nil, errors.New("configuration.Frequency must not be nil")
	}

	return &MicroBatch[Job, JobResult]{
		Configuration:           configuration,
		shutDownChannel:         make(chan bool, 1),
		shutDownCompleteChannel: make(chan bool, 1),
		timerFunc:               func(duration time.Duration) *time.Timer { return time.NewTimer(duration) },
	}, nil
}

// Start starts the batch processor
// reads jobs from the inChannel, passes batches to the BatchProcessor which returns a []JobResult
// will wait until receiving the configured sized (number of jobs) or timeout frequency (after receiving the first message)
// then submit the received batch of Jobs to the BatchProcessor
// JobResults will be sent to the outChannel
// Choice to expose the channels so end user can decide any additional requirements around ordering or synchronicity, this is not
func (microBatch MicroBatch[Job, JobResult]) Start(ctx context.Context, inChannel <-chan Job, outChannel chan<- []JobResult) {
	// make sure to propagate context to goroutines so they get cleaned up
	cancelableContext, cancel := context.WithCancel(ctx)
	shutDown := false

	var batch []Job
	var timerChannel <-chan time.Time
	var wg sync.WaitGroup
	fmt.Println("starting batch processing, listening for jobs...")
	for {
		select {
		case job, more := <-inChannel:
			{
				if more {
					if len(batch) == 0 {
						timerChannel = microBatch.timerFunc(*microBatch.Configuration.Frequency).C
					}
					batch = append(batch, job)
					if len(batch) == microBatch.Configuration.Size {
						timerChannel = nil
						microBatch.sendBatch(cancelableContext, &wg, &batch, outChannel)
						batch = make([]Job, 0)
					}
				} else {
					timerChannel = nil
					shutDown = true
				}
			}
		case <-timerChannel:
			{
				timerChannel = nil
				microBatch.sendBatch(cancelableContext, &wg, &batch, outChannel)
				batch = make([]Job, 0)
			}
		case shutDown = <-microBatch.shutDownChannel:
			{
				timerChannel = nil
				microBatch.sendBatch(cancelableContext, &wg, &batch, outChannel)
				batch = make([]Job, 0)
			}
		}
		if shutDown {
			break
		}
	}

	fmt.Println("shutting down...")
	shutdownTimeout := time.Second * 30
	if microBatch.Configuration.ShutdownTimeout != nil {
		shutdownTimeout = *microBatch.Configuration.ShutdownTimeout
	}

	fmt.Println("waiting for BatchProcessors to complete...")
	ok := waitWithTimeout(&wg, shutdownTimeout) // wait for go funcs to complete
	cancel()                                    // call cancel for ctx
	close(outChannel)                           // close out channel

	if !ok {
		// return error
		microBatch.shutDownCompleteChannel <- false
	} else {
		microBatch.shutDownCompleteChannel <- true
	}
}

func (microBatch MicroBatch[Job, JobResult]) sendBatch(
	ctx context.Context, wg *sync.WaitGroup, batch *[]Job, outChannel chan<- []JobResult,
) {
	if len(*batch) > 0 {
		wg.Add(1)
		go func(toProcess []Job, destination chan<- []JobResult) {
			defer wg.Done()
			result, ok := microBatch.Configuration.BatchProcessor.Process(ctx, toProcess)
			if ok {
				destination <- result
			}
		}(*batch, outChannel)
	}
}

// ShutDown gracefully shut down the MicroBatch, finish processing any currently running BatchProcessor instances
func (microBatch MicroBatch[Job, JobResult]) ShutDown() bool {
	microBatch.shutDownChannel <- true
	ok := <-microBatch.shutDownCompleteChannel

	close(microBatch.shutDownCompleteChannel)
	close(microBatch.shutDownChannel)

	return ok
}

func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan bool)

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-time.After(timeout):
		{
			return false
		}
	case <-done:
		{
			return true
		}
	}
}
