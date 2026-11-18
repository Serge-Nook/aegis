; NSIS installer for «Эгида» (Windows 10/11 x64).
Unicode true
!include "MUI2.nsh"
!include "x64.nsh"

!define APPNAME "Эгида"
!define REGKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Egida"

Name "${APPNAME} ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\Egida"
InstallDirRegKey HKLM "Software\Egida" "InstallDir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
BrandingText "Горшков Сергей Владимирович — nookbat.ru"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "CompanyName" "Горшков Сергей Владимирович, i@nookbat.ru"
VIAddVersionKey "LegalCopyright" "Горшков Сергей Владимирович, https://nookbat.ru/"
VIAddVersionKey "FileDescription" "Установщик ${APPNAME}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"

!define MUI_ICON "${SRCDIR}\egida.ico"
!define MUI_UNICON "${SRCDIR}\egida.ico"
!define MUI_FINISHPAGE_RUN "$INSTDIR\egida.exe"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Russian"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "Требуется 64-разрядная Windows 10 или 11."
    Abort
  ${EndIf}
  SetRegView 64
FunctionEnd

; An update replaces egida.exe: close a running copy first.
!macro CloseRunning
  nsExec::Exec 'taskkill /IM egida.exe /F'
  Pop $0
  Sleep 500
!macroend

Section "Install"
  !insertmacro CloseRunning
  SetOutPath "$INSTDIR"
  File "${SRCDIR}\egida.exe"
  File "${SRCDIR}\egida.ico"
  SetOutPath "$INSTDIR\mesa"
  File "${SRCDIR}\mesa\egida.exe"
  File "${SRCDIR}\mesa\opengl32.dll"
  File "${SRCDIR}\mesa\libgallium_wgl.dll"
  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateDirectory "$SMPROGRAMS\${APPNAME}"
  CreateShortcut "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk" "$INSTDIR\egida.exe"
  CreateShortcut "$SMPROGRAMS\${APPNAME}\Удалить ${APPNAME}.lnk" "$INSTDIR\uninstall.exe"
  CreateShortcut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\egida.exe"
  ; Local-server mode: other workplaces connect over TCP 8080, discovery uses UDP 8089.
  nsExec::Exec 'netsh advfirewall firewall delete rule name="Egida"'
  Pop $0
  nsExec::Exec 'netsh advfirewall firewall add rule name="Egida" dir=in action=allow program="$INSTDIR\egida.exe" enable=yes profile=private,domain'
  Pop $0
  nsExec::Exec 'netsh advfirewall firewall add rule name="Egida" dir=in action=allow program="$INSTDIR\mesa\egida.exe" enable=yes profile=private,domain'
  Pop $0
  WriteRegStr HKLM "Software\Egida" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "${REGKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKLM "${REGKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${REGKEY}" "Publisher" "Горшков Сергей Владимирович"
  WriteRegStr HKLM "${REGKEY}" "URLInfoAbout" "https://nookbat.ru/"
  WriteRegStr HKLM "${REGKEY}" "Contact" "i@nookbat.ru"
  WriteRegStr HKLM "${REGKEY}" "DisplayIcon" "$INSTDIR\egida.ico"
  WriteRegStr HKLM "${REGKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKLM "${REGKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${REGKEY}" "NoRepair" 1
SectionEnd

Function un.onInit
  SetRegView 64
FunctionEnd

Section "Uninstall"
  !insertmacro CloseRunning
  Delete "$INSTDIR\egida.exe"
  Delete "$INSTDIR\egida.ico"
  Delete "$INSTDIR\mesa\egida.exe"
  Delete "$INSTDIR\mesa\opengl32.dll"
  Delete "$INSTDIR\mesa\libgallium_wgl.dll"
  RMDir "$INSTDIR\mesa"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk"
  Delete "$SMPROGRAMS\${APPNAME}\Удалить ${APPNAME}.lnk"
  RMDir "$SMPROGRAMS\${APPNAME}"
  Delete "$DESKTOP\${APPNAME}.lnk"
  nsExec::Exec 'netsh advfirewall firewall delete rule name="Egida"'
  Pop $0
  DeleteRegKey HKLM "${REGKEY}"
  DeleteRegKey HKLM "Software\Egida"
SectionEnd
