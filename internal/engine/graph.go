package engine

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

// TaskGraph 任务图（DAG）
type TaskGraph struct {
	tasks map[string]*Task
	mu    sync.Mutex
}

// NewTaskGraph 创建任务图
func NewTaskGraph() *TaskGraph {
	return &TaskGraph{tasks: map[string]*Task{}}
}

// AddTask 添加任务
func (g *TaskGraph) AddTask(task *Task) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tasks[task.Name] = task
}

// Task 获取任务
func (g *TaskGraph) Task(name string) *Task {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tasks[name]
}

// TopologicalLevels Kahn 分层拓扑排序
func (g *TaskGraph) TopologicalLevels() ([][]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	inDegree := map[string]int{}
	dependents := map[string][]string{}
	for name := range g.tasks {
		inDegree[name] = 0
	}

	for name, task := range g.tasks {
		for dep := range task.DependsOn {
			if _, exists := g.tasks[dep]; exists {
				inDegree[name]++
				dependents[dep] = append(dependents[dep], name)
			}
		}
		for after := range task.MustRunAfter {
			if _, exists := g.tasks[after]; exists {
				inDegree[name]++
				dependents[after] = append(dependents[after], name)
			}
		}
	}

	var levels [][]string
	remaining := map[string]bool{}
	for name := range g.tasks {
		remaining[name] = true
	}

	for len(remaining) > 0 {
		var current []string
		for name := range remaining {
			if inDegree[name] == 0 {
				current = append(current, name)
			}
		}
		if len(current) == 0 {
			var names []string
			for n := range remaining {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("检测到任务循环依赖，剩余任务: %v", names)
		}
		sort.Strings(current)
		levels = append(levels, current)
		for _, name := range current {
			delete(remaining, name)
			for _, dep := range dependents[name] {
				inDegree[dep]--
			}
		}
	}
	return levels, nil
}

// HasCycle 是否含有循环依赖
func (g *TaskGraph) HasCycle() bool {
	_, err := g.TopologicalLevels()
	return err != nil
}

// Execute 执行任务图
func (g *TaskGraph) Execute(ctx *BuildContext, cacheDir string, maxParallel int) *BuildResult {
	result := &BuildResult{}
	levels, err := g.TopologicalLevels()
	if err != nil {
		result.Failed = append(result.Failed, err.Error())
		return result
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		result.Failed = append(result.Failed, err.Error())
		return result
	}

	for _, level := range levels {
		if len(level) == 1 {
			name := level[0]
			task := g.tasks[name]
			if task.IsUpToDate(cacheDir) {
				result.Skipped = append(result.Skipped, name)
				continue
			}
			if task.Execute(ctx) {
				task.SaveCache(cacheDir)
				result.Executed = append(result.Executed, name)
			} else {
				result.Failed = append(result.Failed, name)
				return result
			}
		} else {
			var toRun []*Task
			for _, name := range level {
				task := g.tasks[name]
				if task.IsUpToDate(cacheDir) {
					result.Skipped = append(result.Skipped, name)
				} else {
					toRun = append(toRun, task)
				}
			}
			if len(toRun) == 0 {
				continue
			}
			limit := maxParallel
			if limit <= 0 {
				limit = 4
			}
			if limit > len(toRun) {
				limit = len(toRun)
			}
			sem := make(chan struct{}, limit)
			var wg sync.WaitGroup
			mu := sync.Mutex{}
			for _, task := range toRun {
				wg.Add(1)
				go func(t *Task) {
					defer wg.Done()
					sem <- struct{}{}
					ok := t.Execute(ctx)
					<-sem
					if ok {
						t.SaveCache(cacheDir)
						mu.Lock()
						result.Executed = append(result.Executed, t.Name)
						mu.Unlock()
					} else {
						mu.Lock()
						result.Failed = append(result.Failed, t.Name)
						mu.Unlock()
					}
				}(task)
			}
			wg.Wait()
			if len(result.Failed) > 0 {
				return result
			}
		}
	}
	return result
}

var errCycle = errors.New("task cycle detected")
