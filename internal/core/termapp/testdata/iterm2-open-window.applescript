tell application id "com.googlecode.iterm2"
	set w to (create window with default profile command "/Applications/hopsesh.app/Contents/MacOS/hopsesh terminal-open 0123456789abcdef --hold")
	set s to current session of w
	activate
	return (unique ID of s) & tab & (tty of s)
end tell
