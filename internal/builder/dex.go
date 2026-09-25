package builder

import (
	"fmt"
	"os/exec"
)

// runD8 执行 D8，失败时返回日志（保留头部与尾部，中间省略）
func runD8(d8 string, args []string) error {
	// d8 脚本默认 -Xmx2G，堆偏小时大工程 GC 频繁；前置 -JXmx 覆盖
	cmd := exec.Command(d8, append(d8JVMFlags(), args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := string(out)
		if len(s) > 4000 {
			s = s[:2000] + "\n...\n" + s[len(s)-2000:]
		}
		return fmt.Errorf("%s", s)
	}
	return nil
}
