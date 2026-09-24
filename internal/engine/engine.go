package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// FileSnapshot 文件快照（mtime + size + hash 摘要）
type FileSnapshot struct {
	Path        string
	Mtime       float64
	Size        int64
	ContentHash *string
}

// Matches 比较快照是否一致
func (s *FileSnapshot) Matches(other *FileSnapshot) bool {
	if other == nil {
		return false
	}
	if s.Mtime != other.Mtime || s.Size != other.Size {
		return false
	}
	if s.ContentHash != nil && other.ContentHash != nil {
		return *s.ContentHash == *other.ContentHash
	}
	return true
}

// SnapshotFile 创建文件快照
func SnapshotFile(path string, computeHash bool) *FileSnapshot {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	var h *string
	if computeHash {
		data, err := os.ReadFile(path)
		if err == nil {
			sum := sha256.Sum256(data)
			hexStr := hex.EncodeToString(sum[:])
			h = &hexStr
		}
	}
	return &FileSnapshot{
		Path:        path,
		Mtime:       float64(info.ModTime().UnixNano()) / 1e9,
		Size:        info.Size(),
		ContentHash: h,
	}
}

// SnapshotDir 递归遍历目录生成快照 map（key 为相对路径）
func SnapshotDir(dirPath string, computeHash bool) map[string]*FileSnapshot {
	result := map[string]*FileSnapshot{}
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dirPath, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if snap := SnapshotFile(path, computeHash); snap != nil {
			result[rel] = snap
		}
		return nil
	})
	if err != nil {
		return result
	}
	return result
}

// CompareSnapshots 比较两个快照集合
func CompareSnapshots(old, new map[string]*FileSnapshot, computeHash bool) bool {
	if len(old) != len(new) {
		return false
	}
	for key, oldSnap := range old {
		newSnap, exists := new[key]
		if !exists {
			return false
		}
		if oldSnap.Mtime != newSnap.Mtime || oldSnap.Size != newSnap.Size {
			if computeHash {
				if oldSnap.ContentHash == nil || newSnap.ContentHash == nil {
					return false
				}
				if *oldSnap.ContentHash != *newSnap.ContentHash {
					return false
				}
			} else {
				return false
			}
		}
	}
	return true
}

// BuildContext 构建上下文
type BuildContext struct {
	ProjectDir   string
	BuildDir     string
	CacheDir     string
	AndroidSDK   string
	AndroidJar   string
	BuildTools   string
	JavaHome     string
	ModuleDir    string
	Repositories []string
	Config       interface{}
	StartTime    time.Time
}

// ModuleDirOrProject 返回模块目录（为空时回退项目目录）
func (c *BuildContext) ModuleDirOrProject() string {
	if c.ModuleDir != "" {
		return c.ModuleDir
	}
	return c.ProjectDir
}

// Elapsed 构建已耗时
func (c *BuildContext) Elapsed() time.Duration {
	return time.Since(c.StartTime)
}

// BuildResult 构建结果
type BuildResult struct {
	Executed []string
	Skipped  []string
	Failed   []string
}

// Success 是否成功
func (r *BuildResult) Success() bool {
	return len(r.Failed) == 0
}

type taskCache struct {
	Name           string
	InputSignature string
	Outputs        struct {
		Files []string
		Dirs  []string
	}
	Timestamp float64
}

// Task 任务基类
type Task struct {
	Name          string
	InputFiles    []string
	InputDirs     []string
	OutputFiles   []string
	OutputDirs    []string
	DependsOn     map[string]bool
	MustRunAfter  map[string]bool
	ExecuteFunc   func(ctx *BuildContext) bool
	// 可选：Execute 接口函数
}

// NewTask 创建任务
func NewTask(name string) *Task {
	return &Task{
		Name:         name,
		DependsOn:    map[string]bool{},
		MustRunAfter: map[string]bool{},
	}
}

// AddFileInputs 追加文件输入
func (t *Task) AddFileInputs(files ...string) {
	t.InputFiles = append(t.InputFiles, files...)
}

// AddDirInputs 追加目录输入
func (t *Task) AddDirInputs(dirs ...string) {
	t.InputDirs = append(t.InputDirs, dirs...)
}

// AddFileOutputs 追加文件输出
func (t *Task) AddFileOutputs(files ...string) {
	t.OutputFiles = append(t.OutputFiles, files...)
}

// AddDirOutputs 追加目录输出
func (t *Task) AddDirOutputs(dirs ...string) {
	t.OutputDirs = append(t.OutputDirs, dirs...)
}

// AddDependency 添加依赖
func (t *Task) AddDependency(name string) {
	t.DependsOn[name] = true
}

// ComputeInputSignature 计算输入签名（SHA-256）
func (t *Task) ComputeInputSignature() string {
	hasher := sha256.New()
	type fileEntry struct {
		path  string
		mtime float64
		size  int64
	}
	var entries []fileEntry

	for _, f := range t.InputFiles {
		if info, err := os.Stat(f); err == nil {
			entries = append(entries, fileEntry{
				path:  f,
				mtime: float64(info.ModTime().UnixNano()) / 1e9,
				size:  info.Size(),
			})
		}
	}
	for _, d := range t.InputDirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			filepath.Walk(d, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				rel, rerr := filepath.Rel(d, path)
				if rerr != nil {
					return nil
				}
				entries = append(entries, fileEntry{
					path:  filepath.ToSlash(rel),
					mtime: float64(info.ModTime().UnixNano()) / 1e9,
					size:  info.Size(),
				})
				return nil
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		if entries[i].mtime != entries[j].mtime {
			return entries[i].mtime < entries[j].mtime
		}
		return entries[i].size < entries[j].size
	})

	for _, e := range entries {
		line := fmt.Sprintf("%s|%s|%s", e.path, formatFloat(e.mtime), strconv.FormatInt(e.size, 10))
		hasher.Write([]byte(line))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func (t *Task) cachePath(cacheDir string) string {
	return filepath.Join(cacheDir, t.Name+".json")
}

// IsUpToDate 检查任务是否已是最新
func (t *Task) IsUpToDate(cacheDir string) bool {
	data, err := os.ReadFile(t.cachePath(cacheDir))
	if err != nil {
		return false
	}
	var cached taskCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return false
	}
	if t.ComputeInputSignature() != cached.InputSignature {
		return false
	}
	for _, f := range t.OutputFiles {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	for _, d := range t.OutputDirs {
		if _, err := os.Stat(d); err != nil {
			return false
		}
	}
	return true
}

// SaveCache 保存任务缓存
func (t *Task) SaveCache(cacheDir string) error {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	cache := taskCache{
		Name:           t.Name,
		InputSignature: t.ComputeInputSignature(),
		Timestamp:      float64(time.Now().UnixNano()) / 1e9,
	}
	cache.Outputs.Files = t.OutputFiles
	cache.Outputs.Dirs = t.OutputDirs
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.cachePath(cacheDir), data, 0644)
}

// Execute 执行任务
func (t *Task) Execute(ctx *BuildContext) bool {
	if t.ExecuteFunc != nil {
		return t.ExecuteFunc(ctx)
	}
	return false
}
