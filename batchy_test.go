package batchy

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestNewMicroBatch_ErrorOnNilFrequency(t *testing.T) {
	_, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:           1,
		BatchProcessor: TestBatchProcessor{},
	})
	// in our test this shouldn't happen
	assert.Error(t, err)
	assert.Equal(t, "frequency must not be nil", err.Error())
}

func TestNewMicroBatch_ErrorOnZeroSize(t *testing.T) {
	frequency := 30 * time.Second
	_, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:           0,
		Frequency:      &frequency,
		BatchProcessor: TestBatchProcessor{},
	})
	// in our test this shouldn't happen
	assert.Error(t, err)
	assert.Equal(t, "size must be greater than zero", err.Error())
}

func TestNewMicroBatch_ErrorOnNegativeSize(t *testing.T) {
	frequency := 30 * time.Second // the frequency doesn't matter as our timer channel is mocked
	_, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:           -1,
		Frequency:      &frequency,
		BatchProcessor: TestBatchProcessor{},
	})
	// in our test this shouldn't happen
	assert.Error(t, err)
	assert.Equal(t, "size must be greater than zero", err.Error())
}

func TestBatchyProcess_TimeBatchingSuccess(t *testing.T) {
	timerTriggers := []int{1, 2}
	returnedJobGroups, cleanlyShutdown := runInternalTestBatch(t, 7, 4, &timerTriggers)

	assert.True(t, cleanlyShutdown)
	assert.Len(t, *returnedJobGroups, 3)
	assert.Len(t, (*returnedJobGroups)[0], 2)
	assert.Len(t, (*returnedJobGroups)[1], 1)
	assert.Len(t, (*returnedJobGroups)[2], 4)
}

func TestBatchyProcess_SizeBatchingSuccess(t *testing.T) {
	returnedJobGroups, cleanlyShutdown := runInternalTestBatch(t, 8, 4, nil)

	assert.True(t, cleanlyShutdown)
	assert.Len(t, *returnedJobGroups, 2)
	assert.Len(t, (*returnedJobGroups)[0], 4)
	assert.Len(t, (*returnedJobGroups)[1], 4)
}

func TestBatchyProcess_TimeAndSizeBatchingSuccess(t *testing.T) {
	timerTriggers := []int{6, 8}
	returnedJobGroups, cleanlyShutdown := runInternalTestBatch(t, 9, 4, &timerTriggers)

	assert.True(t, cleanlyShutdown)
	assert.Len(t, *returnedJobGroups, 3)
	assert.Len(t, (*returnedJobGroups)[0], 4)
	assert.Len(t, (*returnedJobGroups)[1], 3)
	assert.Len(t, (*returnedJobGroups)[2], 2)
}

func TestProcessJob_Success(t *testing.T) {
	frequency := 50 * time.Millisecond
	shutdownTimeout := time.Millisecond * 1000
	microBatch, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:            1,
		Frequency:       &frequency,
		BatchProcessor:  TestBatchProcessor{},
		ShutdownTimeout: &shutdownTimeout,
	})
	assert.NoError(t, err)

	microBatch.Start(context.Background())

	lock := sync.Mutex{}
	results := make([]JobResult, 0)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for i := 0; i < 100; i++ {
			result := microBatch.ProcessJob(TestJob{
				Number: i,
			})
			lock.Lock()
			results = append(results, result)
			lock.Unlock()
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		for i := 300; i < 400; i++ {
			result := microBatch.ProcessJob(TestJob{
				Number: i,
			})
			lock.Lock()
			results = append(results, result)
			lock.Unlock()
		}
		wg.Done()
	}()

	wg.Wait()
	cleanlyShutdown := microBatch.ShutDown()

	assert.True(t, cleanlyShutdown)
	assert.Len(t, results, 200)
}

func TestCloseInChannel(t *testing.T) {
	inChannel := make(chan TestJob)
	outChannel := make(chan []TestJobResult, 1)

	frequency := 30 * time.Second // the frequency doesn't matter as our timer channel is mocked
	shutdownTimeout := time.Millisecond * 1000
	microBatch, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:            1,
		Frequency:       &frequency,
		BatchProcessor:  TestNeverDoneProcessor{},
		ShutdownTimeout: &shutdownTimeout,
	})
	// in our test this shouldn't happen
	assert.NoError(t, err)
	_, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		microBatch.startProcessing(context.Background(), inChannel, outChannel, cancel)
		wg.Done()
	}()
	close(inChannel)
	wg.Wait()
	// not sure what to assert here
	// getting passed the wait group would mean the close shutdown cleanly
	cleanlyShutdown := microBatch.ShutDown()
	assert.True(t, cleanlyShutdown)
}

func TestShutdown(t *testing.T) {
	inChannel := make(chan TestJob)
	outChannel := make(chan []TestJobResult, 1)

	frequency := 30 * time.Second // the frequency doesn't matter as our timer channel is mocked
	shutdownTimeout := time.Millisecond * 1000
	microBatch, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:            1,
		Frequency:       &frequency,
		BatchProcessor:  TestNeverDoneProcessor{},
		ShutdownTimeout: &shutdownTimeout,
	})
	// in our test this shouldn't happen
	assert.NoError(t, err)

	fakeTimerChannel := make(chan time.Time)
	microBatch.timerFunc = func(duration time.Duration) *time.Timer {
		return &time.Timer{
			C: fakeTimerChannel,
		}
	}

	go func() {
		_, cancel := context.WithCancel(context.Background())
		microBatch.startProcessing(context.Background(), inChannel, outChannel, cancel)
	}()

	inChannel <- TestJob{
		Number: 0,
	}
	cleanlyShutdown := microBatch.ShutDown()
	assert.False(t, cleanlyShutdown) // shutdown completed due to timeout
}

// uses the internal startProcessing, so we can verify the batches were processed in the way we expect
func runInternalTestBatch(t *testing.T, jobCount, size int, timerTriggers *[]int) (*[][]TestJobResult, bool) {
	jobs := makeTestJobs(jobCount)

	inChannel := make(chan TestJob)
	outChannel := make(chan []TestJobResult, jobCount) // maximum buffer = number of jobs

	frequency := 30 * time.Second // the frequency doesn't matter as our timer channel is mocked
	microBatch, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:           size,
		Frequency:      &frequency,
		BatchProcessor: TestBatchProcessor{},
	})
	// in our test this shouldn't happen
	assert.NoError(t, err)

	fakeTimerChannel := make(chan time.Time, jobCount)
	microBatch.timerFunc = func(duration time.Duration) *time.Timer {
		return &time.Timer{
			C: fakeTimerChannel,
		}
	}

	go func() {
		_, cancel := context.WithCancel(context.Background())
		microBatch.startProcessing(context.Background(), inChannel, outChannel, cancel)
	}()

	// provide jobs synchronously otherwise the return order is not guaranteed
	for _, job := range *jobs {
		fmt.Printf("Providing job %d\n", job.Number)
		inChannel <- job
		if timerTriggers != nil && slices.Contains(*timerTriggers, job.Number) {
			// don't like doing these sleeps :9
			// tiny sleeps to ensure that there is sufficient separation between timeouts and receive
			time.Sleep(5 * time.Millisecond)
			fakeTimerChannel <- time.Now()
			time.Sleep(5 * time.Millisecond)
		}
	}

	jobBatches, cleanlyShutdown := collectJobsBatches(microBatch, outChannel, len(*jobs))

	if fakeTimerChannel != nil {
		close(fakeTimerChannel)
	}

	return jobBatches, cleanlyShutdown
}

func makeTestJobs(number int) *[]TestJob {
	jobs := make([]TestJob, number)
	for i := 0; i < number; i++ {
		jobs[i] = TestJob{
			Number: i,
		}
	}
	return &jobs
}

// collectJobsBatches into a slice
// there is very likely a smarter way to test this, just doing something quick to verify
func collectJobsBatches(microBatch *MicroBatch[TestJob, TestJobResult], outChannel chan []TestJobResult, expectedJobCount int) (*[][]TestJobResult, bool) {
	cleanlyShutdown := false
	jobGroups := make([][]TestJobResult, 0)
	resultCounter := 0
	for jobResult := range outChannel {
		count := len(jobResult)
		// group job results by the timestamp
		jobGroups = append(jobGroups, jobResult)
		resultCounter += count
		if resultCounter >= expectedJobCount {
			cleanlyShutdown = microBatch.ShutDown()
		}
	}
	return &jobGroups, cleanlyShutdown
}

type TestJobResult struct {
	JobNumber  int
	ReturnedAt time.Time
}

func (testJobResult TestJobResult) JobId() string {
	return strconv.Itoa(testJobResult.JobNumber)
}

type TestJob struct {
	Number int
}

func (testJob TestJob) Id() string {
	return strconv.Itoa(testJob.Number)
}

type TestBatchProcessor struct{}

func (TestBatchProcessor) Process(_ context.Context, jobs []TestJob) (jobResults []TestJobResult, ok bool) {
	result := make([]TestJobResult, len(jobs))
	for i, job := range jobs {
		result[i] = TestJobResult{JobNumber: job.Number, ReturnedAt: time.Now()}
	}
	return result, true
}

type TestNeverDoneProcessor struct{}

func (TestNeverDoneProcessor) Process(ctx context.Context, _ []TestJob) (jobResults []TestJobResult, ok bool) {
	select {
	case <-ctx.Done():
		{
			// didn't complete in time
			fmt.Println("cancelled TestNeverDoneProcessor...")
			return nil, false
		}
	case <-time.After(time.Second * 300):
		return nil, true
	}
}
