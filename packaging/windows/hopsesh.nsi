; The hopsesh installer for Windows: per user, no administrator rights. It puts
; hopsesh-app.exe and hopsesh.exe in %LOCALAPPDATA%\Programs\hopsesh (where the install
; script and other machines' hopsesh look for hopsesh.exe), adds a Start menu entry,
; puts the folder on the user's PATH and registers an uninstaller. Updates come from the
; app itself (or hopsesh update); running a newer installer over an install also works.
;
;   makensis -DVERSION=0.3.0 -DNUMVERSION=0.3.0.0 -DARCH=amd64 -DSRC=<folder with both
;     programs> -DOUT=<setup.exe> packaging/windows/hopsesh.nsi

Unicode true
ManifestDPIAware true
RequestExecutionLevel user
SetCompressor /SOLID lzma

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "WordFunc.nsh"

!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\hopsesh"
!define WEBVIEW2_KEY "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
!define WEBVIEW2_KEY_USER "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

Name "hopsesh"
OutFile "${OUT}"
InstallDir "$LOCALAPPDATA\Programs\hopsesh"
BrandingText "hopsesh ${VERSION}"

VIProductVersion "${NUMVERSION}"
VIFileVersion "${NUMVERSION}"
VIAddVersionKey "ProductName" "hopsesh"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "hopsesh installer"
VIAddVersionKey "LegalCopyright" "hopsesh authors, Apache License 2.0"

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_FINISHPAGE_RUN "$INSTDIR\hopsesh-app.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open hopsesh"
!insertmacro MUI_PAGE_LICENSE "${SRC}\LICENSE"
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ReadRegStr $0 HKLM "${WEBVIEW2_KEY}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "${WEBVIEW2_KEY_USER}" "pv"
  ${EndIf}
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    MessageBox MB_YESNO|MB_ICONEXCLAMATION "hopsesh needs the Microsoft Edge WebView2 Runtime, which is not installed. Open its download page?" /SD IDNO IDNO +2
      ExecShell "open" "https://developer.microsoft.com/microsoft-edge/webview2/"
  ${EndIf}
FunctionEnd

Section "hopsesh"
  SetShellVarContext current
  ; An open hopsesh window holds its program file: ask it to close first.
  nsExec::Exec 'taskkill /IM hopsesh-app.exe'
  Sleep 1500
  SetOutPath "$INSTDIR"
  Delete "$INSTDIR\hopsesh-app.exe.old"
  Delete "$INSTDIR\hopsesh.exe.old"
  File "${SRC}\hopsesh-app.exe"
  File "${SRC}\hopsesh.exe"
  File "${SRC}\LICENSE"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortCut "$SMPROGRAMS\hopsesh.lnk" "$INSTDIR\hopsesh-app.exe"

  ; The folder on the user's PATH, so terminals, agents and other machines find hopsesh.
  ReadRegStr $0 HKCU "Environment" "Path"
  StrCpy $2 ";$0;"
  ClearErrors
  ${WordFind} "$2" ";$INSTDIR;" "E+1{" $3
  ${If} ${Errors}
    ${If} $0 == ""
      WriteRegExpandStr HKCU "Environment" "Path" "$INSTDIR"
    ${Else}
      WriteRegExpandStr HKCU "Environment" "Path" "$0;$INSTDIR"
    ${EndIf}
    SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
  ${EndIf}

  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "hopsesh"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "hopsesh"
  WriteRegStr HKCU "${UNINST_KEY}" "URLInfoAbout" "https://github.com/roeehrl/hopsesh"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\hopsesh-app.exe"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  nsExec::Exec 'taskkill /IM hopsesh-app.exe'
  Sleep 1500
  Delete "$INSTDIR\hopsesh-app.exe"
  Delete "$INSTDIR\hopsesh.exe"
  Delete "$INSTDIR\hopsesh-app.exe.old"
  Delete "$INSTDIR\hopsesh.exe.old"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\hopsesh.lnk"

  ReadRegStr $0 HKCU "Environment" "Path"
  StrCpy $0 ";$0;"
  ${un.WordReplace} "$0" ";$INSTDIR;" ";" "+" $0
  ${un.WordReplace} "$0" ";;" ";" "+" $0
  StrCpy $1 $0 1
  ${If} $1 == ";"
    StrCpy $0 $0 "" 1
  ${EndIf}
  StrCpy $1 $0 1 -1
  ${If} $1 == ";"
    StrCpy $0 $0 -1
  ${EndIf}
  WriteRegExpandStr HKCU "Environment" "Path" "$0"
  SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000

  DeleteRegKey HKCU "${UNINST_KEY}"
  ; Settings and session history stay (%APPDATA%\hopsesh, %LOCALAPPDATA%\hopsesh).
SectionEnd
