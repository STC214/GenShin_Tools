#NoEnv
#SingleInstance Force
#InstallKeybdHook
#UseHook On
#MaxThreadsPerHotkey 1
#MaxThreads 10
SendMode Event
SetBatchLines, -1
SetKeyDelay, 15, 15

global GamePIDs := []
Loop, %0%
{
    pid := %A_Index%
    if pid is integer
        GamePIDs.Push(pid + 0)
}
if (GamePIDs.Length() = 0)
    ExitApp

SetTimer, WatchGame, 100
return

$*f::
while GetKeyState("f", "P")
{
    if !GameIsForeground()
        break
    SendEvent, {Blind}{f}
    Sleep, 1
}
return

WatchGame:
if !GameIsRunning()
{
    ExitApp
}
if GameIsForeground()
{
    if A_IsSuspended
        Suspend, Off
}
else
{
    if !A_IsSuspended
        Suspend, On
}
return

GameIsRunning()
{
    global GamePIDs
    for _, pid in GamePIDs
    {
        Process, Exist, %pid%
        if ErrorLevel
            return true
    }
    return false
}

GameIsForeground()
{
    global GamePIDs
    WinGet, activePID, PID, A
    for _, pid in GamePIDs
        if (activePID = pid)
            return true
    return false
}
