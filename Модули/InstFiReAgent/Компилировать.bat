@echo off
setlocal

:: Проверка, что Go установлен
where go >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo Ошибка: Go не найден. Установите Go.
    pause
    exit /b 1
)

:: Компиляция проекта
echo Компиляция проекта...
go build -o InstFiReAgent.exe .

:: Проверка результата компиляции
if %ERRORLEVEL% neq 0 (
    echo Ошибка компиляции.
    pause
    exit /b 1
)

echo.
echo Установщик "InstFiReAgent.exe" успешно скомпилирован!
echo.

pause
