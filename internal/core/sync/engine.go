package sync

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	stdsync "sync"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

const (
	DefaultDebounce = 2 * time.Second
	DefaultMaxRetry = 4
	retryBase       = 500 * time.Millisecond
)

type Engine struct {
	sync     *Service
	diary    *diary.Service
	debounce time.Duration
	maxRetry int
	now      func() time.Time
	sleep    func(time.Duration)
	logger   *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan job
	flush  chan string

	mu    stdsync.Mutex
	locks map[string]*stdsync.Mutex
}

type job struct {
	fs     *filesystem.Service
	months map[string]struct{}
	full   bool
}

type pending struct {
	fs     *filesystem.Service
	months map[string]struct{}
	timer  *time.Timer
}

func NewEngine(syncSvc *Service, diarySvc *diary.Service, debounce time.Duration, now func() time.Time, logger *slog.Logger) *Engine {
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		sync:     syncSvc,
		diary:    diarySvc,
		debounce: debounce,
		maxRetry: DefaultMaxRetry,
		now:      now,
		sleep:    time.Sleep,
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
		jobs:     make(chan job, 64),
		flush:    make(chan string, 64),
		locks:    map[string]*stdsync.Mutex{},
	}
	if diarySvc != nil {
		diarySvc.SetChangeHook(e.NotifyDates)
	}
	go e.loop()
	return e
}

func (e *Engine) Stop() {
	e.cancel()
}

func (e *Engine) NotifyDates(fs *filesystem.Service, dates ...string) {
	if fs == nil {
		return
	}
	payload := job{fs: fs, months: map[string]struct{}{}}
	for _, m := range MonthsFromDates(dates...) {
		payload.months[m] = struct{}{}
	}
	if len(payload.months) == 0 {
		return
	}
	select {
	case <-e.ctx.Done():
	case e.jobs <- payload:
	default:
		e.logger.Warn("sync job queue full; dropping debounce event")
	}
}

func (e *Engine) TriggerFull(ctx context.Context, fs *filesystem.Service) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fs == nil {
		return errors.New("no filesystem bound to the request")
	}
	return e.syncWorkspace(ctx, job{fs: fs, full: true, months: map[string]struct{}{}})
}

func (e *Engine) SyncNow(ctx context.Context, fs *filesystem.Service, dates ...string) error {
	if fs == nil {
		return errors.New("no filesystem bound to the request")
	}
	j := job{fs: fs, months: map[string]struct{}{}}
	for _, m := range MonthsFromDates(dates...) {
		j.months[m] = struct{}{}
	}
	if len(j.months) == 0 {
		j.full = true
	}
	return e.syncWorkspace(ctx, j)
}

func (e *Engine) loop() {
	pendingJobs := map[string]*pending{}
	for {
		select {
		case <-e.ctx.Done():
			for _, p := range pendingJobs {
				if p.timer != nil {
					p.timer.Stop()
				}
			}
			return
		case key := <-e.flush:
			p, ok := pendingJobs[key]
			if !ok {
				continue
			}
			delete(pendingJobs, key)
			if p.timer != nil {
				p.timer.Stop()
			}
			e.run(job{fs: p.fs, months: p.months})
		case j := <-e.jobs:
			key := j.fs.Root()
			if j.full {
				if p, ok := pendingJobs[key]; ok {
					if p.timer != nil {
						p.timer.Stop()
					}
					delete(pendingJobs, key)
				}
				e.run(j)
				continue
			}
			cur, ok := pendingJobs[key]
			if !ok {
				cur = &pending{fs: j.fs, months: map[string]struct{}{}}
				pendingJobs[key] = cur
				root := key
				cur.timer = time.AfterFunc(e.debounce, func() {
					select {
					case <-e.ctx.Done():
					case e.flush <- root:
					}
				})
			} else if cur.timer != nil {
				cur.timer.Reset(e.debounce)
			}
			for m := range j.months {
				cur.months[m] = struct{}{}
			}
		}
	}
}

func (e *Engine) run(j job) {
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
	defer cancel()
	if err := e.syncWorkspace(ctx, j); err != nil && !errors.Is(err, context.Canceled) {
		e.logger.Error("sync failed", "root", j.fs.Root(), "err", err)
	}
}

func (e *Engine) rootLock(root string) *stdsync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.locks[root]
	if !ok {
		l = &stdsync.Mutex{}
		e.locks[root] = l
	}
	return l
}

func (e *Engine) syncWorkspace(ctx context.Context, j job) error {
	lock := e.rootLock(j.fs.Root())
	lock.Lock()
	defer lock.Unlock()

	media, err := e.sync.List(ctx, j.fs)
	if err != nil {
		return err
	}
	entries, err := e.diary.ListCommitted(ctx, j.fs)
	if err != nil {
		return err
	}
	months := make([]string, 0, len(j.months))
	for m := range j.months {
		months = append(months, m)
	}
	var last error
	for _, medium := range media {
		if !medium.Enabled {
			continue
		}
		if err := e.syncMedium(ctx, j.fs, medium, entries, months, j.full); err != nil {
			last = err
			_ = e.sync.RecordFailure(ctx, j.fs, medium.ID, err.Error())
		}
	}
	return last
}

func (e *Engine) syncMedium(ctx context.Context, fs *filesystem.Service, medium PublicMedium, entries []diary.Entry, months []string, full bool) error {
	state, err := e.sync.State(ctx, fs, medium.ID)
	if err != nil {
		return err
	}
	targets := months
	if full || len(targets) == 0 {
		seen := map[string]struct{}{}
		for _, entry := range entries {
			if m, err := MonthKey(entry.DiaryDate); err == nil {
				seen[m] = struct{}{}
			}
		}
		if state != nil {
			for path := range state.Documents {
				if m := monthFromPath(path); m != "" {
					seen[m] = struct{}{}
				}
			}
		}
		targets = make([]string, 0, len(seen))
		for m := range seen {
			targets = append(targets, m)
		}
	}
	var last error
	for _, month := range targets {
		if err := e.syncMonth(ctx, fs, medium.ID, state, entries, month, full); err != nil {
			last = err
		} else {
			state, _ = e.sync.State(ctx, fs, medium.ID)
		}
	}
	return last
}

func (e *Engine) syncMonth(ctx context.Context, fs *filesystem.Service, mediumID string, state *SyncState, entries []diary.Entry, month string, full bool) error {
	path := MonthPath(month)
	monthEntries := EntriesForMonth(entries, month)
	if len(monthEntries) == 0 {
		if state != nil {
			if _, ok := state.Documents[path]; !ok && !full {
				return nil
			}
		}
		return e.withRetry(ctx, func() error {
			_, err := e.sync.Remove(ctx, fs, mediumID, DocumentRef{Kind: DocumentText, Path: path})
			return err
		})
	}
	src := SourceFrom(monthEntries)
	if !full && MonthUnchanged(state, path, src) {
		return nil
	}
	doc := Document{
		Kind:        DocumentText,
		Path:        path,
		Body:        RenderMonth(entries, month),
		ContentType: "text/markdown",
		Source:      src,
	}
	return e.withRetry(ctx, func() error {
		_, err := e.sync.Push(ctx, fs, mediumID, doc)
		return err
	})
}

func (e *Engine) withRetry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt <= e.maxRetry; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = fn()
		if err == nil {
			return nil
		}
		if attempt == e.maxRetry {
			return err
		}
		e.sleep(retryBase << attempt)
	}
	return err
}

func monthFromPath(path string) string {
	const prefix = "diary/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, ".md") {
		return ""
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(path, prefix), ".md")
	_, month, ok := strings.Cut(trimmed, "/")
	if !ok {
		return ""
	}
	return month
}
