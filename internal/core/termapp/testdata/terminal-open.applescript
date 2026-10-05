tell application id "com.apple.Terminal"
	set t to do script "/Applications/hopsesh.app/Contents/MacOS/hopsesh terminal-open 0123456789abcdef"
	activate
	return tty of t
end tell
