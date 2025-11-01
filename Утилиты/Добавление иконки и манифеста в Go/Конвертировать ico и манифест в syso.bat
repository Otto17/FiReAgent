@echo off

:: Конвертирование ico (с любым названием) и манифеста в ".syso"
for %%F in (*.ico) do "%~dp0rsrc.exe" -ico "%%F" -manifest app.manifest -arch amd64

exit