tell application id "com.googlecode.iterm2"
	set w to current window
	if w is missing value then
		set w to (create window with default profile command "/Applications/hopsesh.app/Contents/MacOS/hopsesh terminal-open 0123456789abcdef --hold")
		set s to current session of w
	else
		tell w to set t to (create tab with default profile command "/Applications/hopsesh.app/Contents/MacOS/hopsesh terminal-open 0123456789abcdef --hold")
		set s to current session of t
	end if
	activate
	return (unique ID of s) & tab & (tty of s)
end tell
