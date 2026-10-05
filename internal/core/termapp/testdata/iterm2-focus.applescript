if application id "com.googlecode.iterm2" is not running then return ""
tell application id "com.googlecode.iterm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if (unique ID of s is "w0t1p0-6A1F2C3D-0000-4E5F-9ABC-DEF012345678") and (tty of s is "/dev/ttys004") then
					select w
					select t
					select s
					activate
					return "ok"
				end if
			end repeat
		end repeat
	end repeat
end tell
return ""
