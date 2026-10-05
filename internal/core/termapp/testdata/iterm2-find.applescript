if application id "com.googlecode.iterm2" is not running then return ""
tell application id "com.googlecode.iterm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if tty of s is "/dev/ttys004" then return (unique ID of s) & tab & (tty of s)
			end repeat
		end repeat
	end repeat
end tell
return ""
