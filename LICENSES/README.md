# License inventory

This directory contains verbatim license texts for referenced or redistributed third-party work.

The project references FufuLauncher behavior but redistributes none of its binary or media assets. `golang.org/x/sys` is linked under its BSD 3-Clause license. The portable package includes the project-owner supplied legacy compiled `AHK_F.exe` under the explicit distribution notice in `User-AHK_F-NOTICE.md`. Binary inspection identifies its embedded AutoHotkey v1.0.48.05 runtime, so the matching GPL-2.0 text is included here and the exact upstream source archive is included under `SOURCES/`. Future DLLs, plugins, fonts, icons, sounds and generated protocol bindings must be added to the inventory before they are accepted.
