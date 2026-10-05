if application id "com.apple.Terminal" is not running then return ""
tell application id "com.apple.Terminal"
	repeat with w in windows
		repeat with t in tabs of w
			if tty of t is "/dev/ttys004" then
				set selected tab of w to t
				set frontmost of w to true
				activate
				return "ok"
			end if
		end repeat
	end repeat
end tell
return ""
