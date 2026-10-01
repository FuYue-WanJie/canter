// Package version 保存 Canter 的产品版本信息。
package version

// Version 是当前发布版本标识。
const Version = "Preview 1"

// Name 是产品名称。
const Name = "Canter"

// String 返回 "Canter Preview 1" 形式的完整标识。
func String() string {
	return Name + " " + Version
}
