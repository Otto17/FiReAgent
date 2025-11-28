@echo off

:: Конвертирование ico (с любым названием) в syso
for %%F in (*.ico) do "%~dp0rsrc.exe" -ico "%%F"

exit