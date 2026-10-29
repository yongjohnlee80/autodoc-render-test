//go:build !linux

package scene

import "errors"

// Vulkan is Linux's here: elsewhere NewVulkan says so and the plugin keeps the CPU renderer.
type Vulkan struct{}

// NewVulkan is unsupported off Linux.
func NewVulkan() (*Vulkan, error) {
	return nil, errors.New("vulkan: this build renders on the CPU only")
}

func (v *Vulkan) Name() string   { return "vulkan" }
func (v *Vulkan) Device() string { return "" }
func (v *Vulkan) Draw(float64, []byte, int, int, int, bool) error {
	return errors.New("vulkan: unsupported")
}
func (v *Vulkan) Close() {}
