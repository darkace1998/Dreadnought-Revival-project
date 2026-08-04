@echo off
setlocal enabledelayedexpansion

rem Build dn_host_loadout.dll with MSVC.
rem
rem Deliberately standalone: this is not in go.work, scripts/setup.sh does not
rem call it, and nothing in the Go stack depends on its output. A checkout with
rem this directory deleted still builds and still runs a match.
rem
rem Usage:  build.bat            (from a normal shell -- finds MSVC itself)
rem         build.bat            (from a Developer Command Prompt -- uses it)

if defined VCINSTALLDIR goto :have_msvc

rem vswhere lives under "Program Files (x86)". Those parentheses close a
rem parenthesised block early if the path is expanded inside one, so cd to the
rem directory first and invoke it by bare name.
set "VSINSTALLERDIR=%ProgramFiles(x86)%\Microsoft Visual Studio\Installer"
if not exist "%VSINSTALLERDIR%\vswhere.exe" (
  echo ERROR: vswhere.exe not found. Install Visual Studio Build Tools with the
  echo        "Desktop development with C++" workload, or run this from a
  echo        Developer Command Prompt for VS.
  exit /b 1
)

pushd "%VSINSTALLERDIR%"
for /f "usebackq tokens=*" %%i in (`.\vswhere.exe -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath`) do set "VSPATH=%%i"
popd
if not defined VSPATH (
  echo ERROR: no Visual Studio installation with the C++ x64 toolset was found.
  exit /b 1
)

rem vcvars64.bat invokes vswhere by bare name internally and leaks a harmless
rem "not recognized" line to stderr on some installs; it still sets the
rem environment correctly, so both streams are discarded here.
call "%VSPATH%\VC\Auxiliary\Build\vcvars64.bat" >nul 2>nul
if errorlevel 1 (
  echo ERROR: vcvars64.bat failed.
  exit /b 1
)

:have_msvc

set "ROOT=%~dp0"
set "OUT=%ROOT%build"
set "MH=%ROOT%third_party\minhook"

if not exist "%OUT%" mkdir "%OUT%"
pushd "%OUT%" || exit /b 1

echo Building dn_host_loadout.dll ...

cl /nologo /c /O2 /MT /W3 /D_CRT_SECURE_NO_WARNINGS /DWIN32_LEAN_AND_MEAN ^
   /I"%MH%\include" ^
   "%MH%\src\buffer.c" "%MH%\src\hook.c" "%MH%\src\trampoline.c" ^
   "%MH%\src\hde\hde64.c" ^
   || (popd & exit /b 1)

rem /EHa, not /EHsc: the loadout resolution uses __try/__except around calls
rem into the game, and structured exception handling needs the asynchronous
rem model to be caught rather than terminate the host.
cl /nologo /c /O2 /MT /W4 /EHa /std:c++17 /D_CRT_SECURE_NO_WARNINGS ^
   /DWIN32_LEAN_AND_MEAN ^
   /I"%MH%\include" ^
   "%ROOT%src\dn_host_loadout.cpp" ^
   || (popd & exit /b 1)

link /nologo /DLL /OUT:dn_host_loadout.dll ^
     dn_host_loadout.obj buffer.obj hook.obj trampoline.obj hde64.obj ^
     kernel32.lib user32.lib ^
     || (popd & exit /b 1)

popd

echo.
echo Built %OUT%\dn_host_loadout.dll
echo.
echo To deploy, see README.md. In short: copy it next to
echo DreadGame-Win64-Shipping.exe as wer.dll, and create an empty
echo dn_server_loadout.txt in the same directory to switch it on.
endlocal
