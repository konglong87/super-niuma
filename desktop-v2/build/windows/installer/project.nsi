# Based on Wails v2.10.2 pkg/buildassets/build/windows/installer/project.nsi.
# Wails generates wails_tools.nsh and tmp/WebView2 bootstrapper beside this file.
Unicode true
!define PRODUCT_EXECUTABLE "niuma-desktop.exe"
!define SIDECAR_EXECUTABLE "niuma.exe"
# Keep the uninstall registry key stable and aligned with the release acceptance checks.
!define UNINST_KEY_NAME "niuma"
!include "wails_tools.nsh"

# The sidecar build is currently amd64 only. Never silently ship it for ARM64.
!ifndef ARG_WAILS_AMD64_BINARY
    !error "niuma requires a Windows amd64 build"
!endif
!ifdef ARG_WAILS_ARM64_BINARY
    !error "niuma does not yet package an ARM64 sidecar"
!endif

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName" "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright" "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName" "${INFO_PRODUCTNAME}"
ManifestDPIAware true

!include "MUI.nsh"
!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
ShowInstDetails show

Function .onInit
    !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext
    !insertmacro wails.webview2runtime
    SetOutPath "$INSTDIR"
    !insertmacro wails.files
    # Paths are relative to build/windows/installer, not the repository root.
    # No /nonfatal: a missing runtime must fail packaging.
    File "/oname=${SIDECAR_EXECUTABLE}" "..\..\..\${SIDECAR_EXECUTABLE}"
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols
    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext
    # Remove only owned payload. Preserve user files, SQLite, settings and WebView2 data.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\${SIDECAR_EXECUTABLE}"
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols
    !insertmacro wails.deleteUninstaller
    RMDir "$INSTDIR"
SectionEnd
