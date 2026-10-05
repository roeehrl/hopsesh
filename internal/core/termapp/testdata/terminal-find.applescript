if application id "com.apple.Terminal" is not running then return ""
tell application id "com.apple.Terminal"
	repeat with w in windows
		repeat with t in tabs of w
			if tty of t is "/dev/ttys004" then return tty of t
		end repeat
	end repeat
end tell
return ""
