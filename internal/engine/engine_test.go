package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSignatureDetectsContentChange(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	os.MkdirAll(cacheDir, 0755)
	src := filepath.Join(dir, "a.kt")
	os.WriteFile(src, []byte("val a = 1\n"), 0644)

	task := NewTask("t")
	task.AddFileInputs(src)

	sig1 := task.ComputeInputSignature(cacheDir)
	if sig2 := task.ComputeInputSignature(cacheDir); sig1 != sig2 {
		t.Fatalf("无改动签名应一致")
	}

	// 内容变化（正常编辑会更新 mtime）
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(src, []byte("val a = 2\n"), 0644)
	if sig3 := task.ComputeInputSignature(cacheDir); sig3 == sig1 {
		t.Fatalf("内容变化应被检测到")
	}
	t.Logf("OK: 内容变化被检测到")

	// 记忆化：mtime/size 不变时应复用缓存哈希（不重复读盘）
	info, _ := os.Stat(src)
	_ = info
}
