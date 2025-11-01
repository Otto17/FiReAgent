# Получаем путь к папке, где лежит скрипт
$scriptDir = $PSScriptRoot
if (-not $scriptDir) {
    $scriptDir = (Get-Location).Path
}

# Задаём имена файлов (без полных путей)
$sourcePfxName = "CryptoAgent.pfx"
$outputPfxName = $sourcePfxName  # Новый файл будет иметь исходное имя
$oldPfxName = "CryptoAgent_old.pfx"  # Старый файл будет переименован

# Полные пути к файлам в папке со скриптом
$sourcePfxPath = Join-Path -Path $scriptDir -ChildPath $sourcePfxName
$outputPfxPath = Join-Path -Path $scriptDir -ChildPath $outputPfxName
$oldPfxPath = Join-Path -Path $scriptDir -ChildPath $oldPfxName

# Проверяем существование исходного файла
if (-not (Test-Path -Path $sourcePfxPath)) {
    Write-Host "Ошибка: Файл '$sourcePfxName' не найден в папке '$scriptDir'!" -ForegroundColor Red
    Read-Host -Prompt "Нажмите Enter для выхода..."
    exit 1
}

# Запрашиваем пароль у пользователя
$password = Read-Host "Введите пароль для сертификата '$sourcePfxName'" -AsSecureString
$passwordPtr = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($password)
$plainPassword = [System.Runtime.InteropServices.Marshal]::PtrToStringAuto($passwordPtr)

try {
    # Загружаем сертификат с паролем
    $pfx = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2(
        $sourcePfxPath,
        $plainPassword,
        [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::Exportable
    )

    # Экспортируем сертификат без пароля
    $pfxBytes = $pfx.Export(
        [System.Security.Cryptography.X509Certificates.X509ContentType]::Pfx,
        $null
    )

    # Переименовываем исходный файл
    if (Test-Path -Path $oldPfxPath) {
        Remove-Item -Path $oldPfxPath -Force
    }
    Rename-Item -Path $sourcePfxPath -NewName $oldPfxName

    # Сохраняем новый файл с исходным именем
    [IO.File]::WriteAllBytes($outputPfxPath, $pfxBytes)
    Write-Host "Сертификат успешно сохранён как '$outputPfxName' без пароля! Исходный файл переименован в '$oldPfxName'." -ForegroundColor Green
}
catch {
    Write-Host "Ошибка: $($_.Exception.Message)" -ForegroundColor Red
}
finally {
    # Очищаем память от пароля
    [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passwordPtr)
    Read-Host -Prompt "Нажмите Enter для выхода..."
}
