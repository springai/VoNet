package singbox

import (
	"bufio"
	"io"
	"sync"
	"time"
)

// LogEntry is a single line of sing-box output.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Source  string    `json:"source"` // "stdout" or "stderr"
	Message string    `json:"msg"`
}

const (
	maxLogLines    = 500
	maxSubscribers = 8
)

// LogBuffer captures sing-box process output and distributes it to subscribers.
type LogBuffer struct {
	mu          sync.RWMutex
	lines       []LogEntry
	subscribers map[int]chan LogEntry
	nextSubID   int
}

func NewLogBuffer() *LogBuffer {
	return &LogBuffer{
		lines:       make([]LogEntry, 0, maxLogLines),
		subscribers: make(map[int]chan LogEntry),
	}
}

// Write implements io.Writer so it can be used as cmd.Stdout / cmd.Stderr.
// It splits the input into lines and appends each as a LogEntry.
func (lb *LogBuffer) Write(p []byte) (int, error) {
	lb.Push(string(p), "stdout")
	return len(p), nil
}

// Push adds a message to the log buffer and notifies subscribers.
func (lb *LogBuffer) Push(msg string, source string) {
	entry := LogEntry{
		Time:    time.Now(),
		Source:  source,
		Message: msg,
	}
	lb.mu.Lock()
	// Ring buffer: drop oldest when full.
	if len(lb.lines) >= maxLogLines {
		lb.lines = lb.lines[1:]
	}
	lb.lines = append(lb.lines, entry)
	// Notify subscribers (non-blocking send).
	for _, ch := range lb.subscribers {
		select {
		case ch <- entry:
		default:
			// Subscriber too slow; drop this entry for them.
		}
	}
	lb.mu.Unlock()
}

// Recent returns the last n log entries.
func (lb *LogBuffer) Recent(n int) []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	total := len(lb.lines)
	if n > total {
		n = total
	}
	result := make([]LogEntry, n)
	copy(result, lb.lines[total-n:])
	return result
}

// All returns all buffered log entries.
func (lb *LogBuffer) All() []LogEntry {
	return lb.Recent(maxLogLines)
}

// Subscribe returns a channel that receives new log entries and an unsubscribe function.
func (lb *LogBuffer) Subscribe(buffer int) (chan LogEntry, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan LogEntry, buffer)
	lb.mu.Lock()
	id := lb.nextSubID
	lb.nextSubID++
	lb.subscribers[id] = ch
	lb.mu.Unlock()
	unsub := func() {
		lb.mu.Lock()
		delete(lb.subscribers, id)
		close(ch)
		lb.mu.Unlock()
	}
	return ch, unsub
}

// PipeLines reads from r line by line and pushes each line to the log buffer.
func (lb *LogBuffer) PipeLines(r io.Reader, source string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lb.Push(scanner.Text(), source)
	}
}