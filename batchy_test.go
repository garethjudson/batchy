package batchy

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"slices"
	"testing"
	"time"
)

func TestNewMicroBatch_ErrorOnNilFrequency(t *testing.T) {
	_, err := NewMicroBatch(MicroBatchConfig[TestJob, TestJobResult]{
		Size:           0,
		Frequency:      nil,
		BatchProcessor: TestBatchProcessor{},
	})
	// in our test this shouldn't happen
	assert.Error(t, err)
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
}

func TestBatchyProcess_TimeBatchingSuccess(t *testing.T) {
	timerTriggers := []int{1, 2}
	returnedJobGroups, ok := runTestBatch(t, 7, 4, &timerTriggers)

	assert.True(t, ok)
	assert.Len(t, *returnedJobGroups, 3)
	assert.Len(t, (*returnedJobGroups)[0], 2)
	assert.Len(t, (*returnedJobGroups)[1], 1)
	assert.Len(t, (*returnedJobGroups)[2], 4)
}

func TestBatchyProcess_SizeBatchingSuccess(t *testing.T) {
	returnedJobGroups, ok := runTestBatch(t, 8, 4, nil)

	assert.True(t, ok)
	assert.Len(t, *returnedJobGroups, 2)
	assert.Len(t, (*returnedJobGroups)[0], 4)
	assert.Len(t, (*returnedJobGroups)[1], 4)
}

func TestBatchyProcess_TimeAndSizeBatchingSuccess(t *testing.T) {
	timerTriggers := []int{6, 8}
	returnedJobGroups, ok := runTestBatch(t, 9, 4, &timerTriggers)

	assert.True(t, ok)
	assert.Len(t, *returnedJobGroups, 3)
	assert.Len(t, (*returnedJobGroups)[0], 4)
	assert.Len(t, (*returnedJobGroups)[1], 3)
	assert.Len(t, (*returnedJobGroups)[2], 2)
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
		microBatch.Start(context.Background(), inChannel, outChannel)
	}()

	inChannel <- TestJob{
		Number: 0,
	}
	ok := microBatch.ShutDown()
	assert.False(t, ok) // shutdown completed due to timeout
}

func runTestBatch(t *testing.T, jobCount, size int, timerTriggers *[]int) (*[][]TestJobResult, bool) {
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
		microBatch.Start(context.Background(), inChannel, outChannel)
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

	jobBatches, ok := collectJobsBatches(microBatch, outChannel, len(*jobs))

	if fakeTimerChannel != nil {
		close(fakeTimerChannel)
	}

	return jobBatches, ok
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
	ok := false
	jobGroups := make([][]TestJobResult, 0)
	resultCounter := 0
	for jobResult := range outChannel {
		count := len(jobResult)
		// group job results by the timestamp
		jobGroups = append(jobGroups, jobResult)
		resultCounter += count
		if resultCounter >= expectedJobCount {
			ok = microBatch.ShutDown()
		}
	}
	return &jobGroups, ok
}

type TestJobResult struct {
	JobNumber  int
	ReturnedAt time.Time
}

type TestJob struct {
	Number int
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
