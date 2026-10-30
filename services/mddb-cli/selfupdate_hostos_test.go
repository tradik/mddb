package main

// The self-update tests describe the flow on a platform with release
// archives. hostOS is the real GOOS, so on the windows-latest CI runner every
// one of them met the Windows refusal (#374) instead of the flow it tests.
// They run as linux; the Windows refusal test sets hostOS itself.
func init() { hostOS = "linux" }
