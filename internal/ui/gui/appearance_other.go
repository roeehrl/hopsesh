//go:build !darwin

package gui

// The Wails version in use has no public runtime native-theme setter on these
// platforms. Window content follows the preference; decorations follow the OS.
func nativeAppearance(string) {}
