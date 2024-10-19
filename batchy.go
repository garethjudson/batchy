package batchy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Job interface {
	Id() string
}

type JobResult interface {
	JobId() string
}

// MicroBatch encapsulates the methods for Starting and Shutting down the batch processing
// Job and JobResult generics are used to allow input or output of any types
type MicroBatch[T Job, V JobResult] struct {
	Configuration           MicroBatchConfig[T, V]
	shutDownChannel         chan bool
	shutDownCompleteChannel chan bool
	timerFunc               func(duration time.Duration) *time.Timer
	insertLock              *sync.Mutex
	inChannel               chan T
	jobToResultChannel      map[string]chan V
}

// MicroBatchConfig contains setup configuration for a MicroBatch
// Size The maximum size of batch, once reached the batch is sent to the processor
// Frequency how often to send a micro batch to the processor
// BatchProcessor a processor that will do some work on the inbound Jobs, returning a JobResult
// InChannel this is where Jobs should be sent, these will be batched and sent to the BatchProcessor as Frequency is reached
type MicroBatchConfig[T Job, V JobResult] struct {
	Size            int
	Frequency       *time.Duration
	BatchProcessor  BatchProcessor[T, V]
	ShutdownTimeout *time.Duration // will default to 30 seconds if not provided
}

// BatchProcessor used by the MicroBatch to process an individual job and returns a job result
type BatchProcessor[T Job, V JobResult] interface {
	// Process the function used to process a Job (interface {}) and return a JobResult (interface {}) and an ok bool flag to return if this function was cancelled
	// ctx is used so that the caller can conveniently use any appropriate context in the implemented Process function
	// This is the context that was passed into Start function
	// Monitor ctx.Done so go routines will get cleaned up, and return ok == false if job was not finished
	Process(ctx context.Context, job []T) (jobResults []V, ok bool)
}

// NewMicroBatch constructs a new MicroBatch given the appropriate arguments
func NewMicroBatch[T Job, V JobResult](configuration MicroBatchConfig[T, V]) (*MicroBatch[T, V], error) {
	// could replace with sensible defaults
	if configuration.Size < 1 {
		return nil, errors.New("size must be greater than zero")
	}
	if configuration.Frequency == nil {
		return nil, errors.New("frequency must not be nil")
	}

	return &MicroBatch[T, V]{
		Configuration:           configuration,
		shutDownChannel:         make(chan bool, 1),
		shutDownCompleteChannel: make(chan bool, 1),
		timerFunc:               func(duration time.Duration) *time.Timer { return time.NewTimer(duration) },
		inChannel:               make(chan T, 10),
		jobToResultChannel:      make(map[string]chan V),
		insertLock:              &sync.Mutex{},
	}, nil
}

// Start starts the batch processor
// starts reading jobs when the ProcessJob is called
// passes batches to the BatchProcessor and then marries the result to back to the job so it can be returned to the caller
func (microBatch MicroBatch[T, V]) Start(ctx context.Context) {
	fmt.Println("starting batch processing...")
	cancelableContext, cancel := context.WithCancel(ctx)
	outChannel := make(chan []V, 10)

	fmt.Println("starting listening for results...")
	go microBatch.startReadingResults(cancelableContext, outChannel)

	fmt.Println("starting listening for jobs...")
	go microBatch.startProcessing(cancelableContext, microBatch.inChannel, outChannel, cancel)
}

func (microBatch MicroBatch[T, V]) startReadingResults(ctx context.Context, outChannel <-chan []V) {
	shutdown := false
	for {
		select {
		case jobResults, more := <-outChannel:
			{
				if more {
					for _, jobResult := range jobResults {
						microBatch.insertLock.Lock()
						channel := microBatch.jobToResultChannel[jobResult.JobId()]
						if channel != nil {
							channel <- jobResult
							close(channel)
							delete(microBatch.jobToResultChannel, jobResult.JobId())
						}
						microBatch.insertLock.Unlock()
					}
				} else {
					shutdown = true
				}
			}
		case <-ctx.Done():
			{
				shutdown = true
			}
		}
		if shutdown {
			println("Exited read results")
			break
		}
	}
}

func (microBatch MicroBatch[T, V]) startProcessing(ctx context.Context, inChannel <-chan T, outChannel chan<- []V, cancel func()) {
	// make sure to propagate context to goroutines so they get cleaned up
	shutDown := false

	var batch []T
	var timerChannel <-chan time.Time
	var wg sync.WaitGroup

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
						microBatch.sendBatch(ctx, &wg, &batch, outChannel)
						batch = make([]T, 0)
					}
				} else {
					timerChannel = nil
					shutDown = true
				}
			}
		case <-timerChannel:
			{
				timerChannel = nil
				microBatch.sendBatch(ctx, &wg, &batch, outChannel)
				batch = make([]T, 0)
			}
		case shutDown = <-microBatch.shutDownChannel:
			{
				timerChannel = nil
				microBatch.sendBatch(ctx, &wg, &batch, outChannel)
				batch = make([]T, 0)
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
	completedWithinTimeout := waitWithTimeout(&wg, shutdownTimeout) // wait for go funcs to complete
	fmt.Println("waiting BatchProcessors complete")
	cancel() // call cancel for ctx
	close(outChannel)
	microBatch.shutDownCompleteChannel <- completedWithinTimeout
}

func (microBatch MicroBatch[T, V]) sendBatch(
	ctx context.Context, wg *sync.WaitGroup, batch *[]T, outChannel chan<- []V,
) {
	if len(*batch) > 0 {
		wg.Add(1)
		go func(toProcess []T, destination chan<- []V) {
			defer wg.Done()
			result, ok := microBatch.Configuration.BatchProcessor.Process(ctx, toProcess)
			if ok {
				destination <- result
			}
		}(*batch, outChannel)
	}
}

func (microBatch MicroBatch[T, V]) ProcessJob(job T) V {
	doneChannel := make(chan V, 1)
	microBatch.insertLock.Lock()
	microBatch.jobToResultChannel[job.Id()] = doneChannel
	microBatch.insertLock.Unlock()
	microBatch.inChannel <- job
	result := <-doneChannel
	return result
}

// ShutDown gracefully shut down the MicroBatch, finish processing any currently running BatchProcessor instances
func (microBatch MicroBatch[T, V]) ShutDown() bool {
	microBatch.shutDownChannel <- true
	cleanlyShutdown := <-microBatch.shutDownCompleteChannel

	close(microBatch.shutDownCompleteChannel)
	close(microBatch.shutDownChannel)

	return cleanlyShutdown
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
			fmt.Printf("BatchProcessors did not complete in %d milliseconds, could likely there are leaky go routines\n", timeout.Milliseconds())
			return false
		}
	case <-done:
		{
			fmt.Printf("BatchProcessors completed in time\n")
			return true
		}
	}
}
