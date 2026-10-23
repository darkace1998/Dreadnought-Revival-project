; dn-install.exe: puts dn-launcher in place of Steam's DreadnoughtLauncher.exe
; (original kept as DreadnoughtLauncher.exe.old), adds the server CA to the
; current user's Trusted Root store. Built by scripts/build-installer.sh.

Unicode true
!include MUI2.nsh

!define GAMEEXE "DreadGame\DreadGame\Binaries\Win64\DreadGame-Win64-Shipping.exe"
!define UNINST  "Software\Microsoft\Windows\CurrentVersion\Uninstall\DreadnoughtRevival"

Name "Dreadnought Revival"
OutFile "..\..\run\dn-install.exe"
RequestExecutionLevel user
InstallDir "$PROGRAMFILES32\Steam\steamapps\common\Dreadnought\"
BrandingText " "

!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING

!if /FileExists "payload\ca.crt"
  !define WHAT "Replaces the Dreadnought launcher with the Revival launcher and installs the server certificate."
!else
  !define WHAT "Replaces the Dreadnought launcher with the Revival launcher."
!endif
!define MUI_WELCOMEPAGE_TEXT "${WHAT}$\r$\n$\r$\nThe original launcher is kept as DreadnoughtLauncher.exe.old.$\r$\n$\r$\nClose Steam's Dreadnought before continuing."
!insertmacro MUI_PAGE_WELCOME
!define MUI_DIRECTORYPAGE_TEXT_TOP "Dreadnought game folder (contains DreadnoughtLauncher.exe and DreadGame)."
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_TEXT "Done. Start Dreadnought from Steam."
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ReadRegStr $0 HKCU "Software\Valve\Steam" "SteamPath"
  StrCmp $0 "" done
  ; Steam stores it as c:/program files (x86)/steam
  StrCpy $1 ""
  StrCpy $2 0
slash:
  StrCpy $3 $0 1 $2
  StrCmp $3 "" slashed
  StrCmp $3 "/" 0 +2
  StrCpy $3 "\"
  StrCpy $1 "$1$3"
  IntOp $2 $2 + 1
  Goto slash
slashed:
  StrCpy $0 $1
  IfFileExists "$0\steamapps\common\Dreadnought\${GAMEEXE}" 0 done
  StrCpy $INSTDIR "$0\steamapps\common\Dreadnought"
done:
FunctionEnd

; Next stays disabled until the folder holds the game.
Function .onVerifyInstDir
  IfFileExists "$INSTDIR\${GAMEEXE}" +2
  Abort
FunctionEnd

Section
  SetOutPath "$INSTDIR"
  IfFileExists "$INSTDIR\DreadnoughtLauncher.exe.old" keep
  IfFileExists "$INSTDIR\DreadnoughtLauncher.exe" 0 keep
  ClearErrors
  Rename "$INSTDIR\DreadnoughtLauncher.exe" "$INSTDIR\DreadnoughtLauncher.exe.old"
  IfErrors 0 +3
  MessageBox MB_ICONSTOP "Could not rename DreadnoughtLauncher.exe. Close the game, or run the installer as administrator."
  Abort
  DetailPrint "DreadnoughtLauncher.exe -> DreadnoughtLauncher.exe.old"
  Goto write
keep:
  DetailPrint "DreadnoughtLauncher.exe.old exists, kept"
write:
  SetOverwrite on
  File "/oname=DreadnoughtLauncher.exe" "payload\dn-launcher.exe"
!if /FileExists "payload\dn-launcher.json"
  File "payload\dn-launcher.json"
!endif
!if /FileExists "payload\ca.crt"
  File "payload\ca.crt"
  DetailPrint "Installing server certificate (confirm the Windows prompt)"
  nsExec::ExecToLog 'certutil -user -f -addstore Root "$INSTDIR\ca.crt"'
  Pop $0
  StrCmp $0 "0" +2
  MessageBox MB_ICONEXCLAMATION "Certificate not installed (code $0). The launcher will offer it again on start."
!endif

  WriteUninstaller "$INSTDIR\dn-uninstall.exe"
  WriteRegStr HKCU "${UNINST}" "DisplayName" "Dreadnought Revival launcher"
  WriteRegStr HKCU "${UNINST}" "UninstallString" '"$INSTDIR\dn-uninstall.exe"'
  WriteRegStr HKCU "${UNINST}" "DisplayIcon" "$INSTDIR\DreadnoughtLauncher.exe"
  WriteRegDWORD HKCU "${UNINST}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  IfFileExists "$INSTDIR\DreadnoughtLauncher.exe.old" 0 +4
  Delete "$INSTDIR\DreadnoughtLauncher.exe"
  Rename "$INSTDIR\DreadnoughtLauncher.exe.old" "$INSTDIR\DreadnoughtLauncher.exe"
  DetailPrint "Restored DreadnoughtLauncher.exe"
  Delete "$INSTDIR\dn-launcher.json"
  Delete "$INSTDIR\ca.crt"
  Delete "$INSTDIR\dn-uninstall.exe"
  DeleteRegKey HKCU "${UNINST}"
  DetailPrint "Server certificate left in Trusted Root (certmgr.msc to remove)"
SectionEnd
