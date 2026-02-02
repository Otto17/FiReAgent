// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"bufio"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Поместить файлы портативного OpenSSL в папку "openssl" рядом с main.go.
// Нужны эти файлы (для OpenSSL 3.x):
// - openssl/openssl.exe
// - openssl/openssl.cnf
// - openssl/legacy.dll
// - openssl/libssl-3-x64.dll
// - openssl/libcrypto-3-x64.dll

//go:embed openssl/*
var embeddedFS embed.FS // EmbeddedFS содержит встроенные ресурсы папки "openssl"

const CurrentVersion = "02.02.26" // Текущая версия GenCryAgent в формате "дд.мм.гг"

func main() {
	// Отображает версию GenCryAgent
	if len(os.Args) >= 2 && strings.EqualFold(os.Args[1], "--version") {
		fmt.Printf("Версия \"GenCryAgent\": %s\n", CurrentVersion)
		return
	}

	fmt.Print("Генерация PFX сертификата для FiReAgent.\n\n")
	defer waitForEnter()

	exePath, err := os.Executable()
	if err != nil {
		fail("Не удалось определить путь к исполняемому файлу: %v", err)
		return
	}
	exeDir := filepath.Dir(exePath)
	destPFX := filepath.Join(exeDir, "CryptoAgent.pfx")

	// Временная папка для изоляции OpenSSL и промежуточных файлов
	tmpDir, err := os.MkdirTemp("", "GenCryAgent-*")
	if err != nil {
		fail("Не удалось создать временную папку: %v", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	// Распаковывает встроенные файлы OpenSSL
	files := []string{
		"openssl.exe",
		"openssl.cnf",
		"libssl-3-x64.dll",
		"libcrypto-3-x64.dll",
		"legacy.dll",
	}
	for _, name := range files {
		if err := extractOpenssl("openssl/"+name, filepath.Join(tmpDir, name)); err != nil {
			fail("Не удалось распаковать %s: %v", name, err)
			return
		}
	}

	// Генерация ключа и самоподписанного сертификата
	fmt.Println("1/3 Генерация ключа и самоподписанного сертификата...")
	reqArgs := []string{
		"req",
		"-x509",
		"-newkey", "rsa:2048",
		"-keyout", "key_CryptoAgent.pem",
		"-out", "crt_CryptoAgent.pem",
		"-days", "3650",
		"-nodes",
		"-subj", "/CN=CryptoAgent",
		"-config", "openssl.cnf",
	}
	if err := runOpenSSL(tmpDir, reqArgs...); err != nil {
		fail("Ошибка при генерации ключа/сертификата: %v", err)
		return
	}

	// Создание PFX (совместимый с Windows 8.1: TripleDES-SHA1)
	fmt.Println("2/3 Экспорт в PFX (совместимость с Windows 8.1: \"TripleDES-SHA1\")...")
	pfxArgs := []string{
		"pkcs12",
		"-export",
		"-out", "CryptoAgent.pfx",
		"-inkey", "key_CryptoAgent.pem",
		"-in", "crt_CryptoAgent.pem",
		"-passout", "pass:FiReAgent",
		"-provider-path", ".", // Указывает OpenSSL искать "legacy.dll" в текущей рабочей папке (tmpDir)

		// Настройки для совместимости с Win 8.1
		"-legacy",
		"-keypbe", "PBE-SHA1-3DES",
		"-certpbe", "PBE-SHA1-3DES",
		"-macalg", "sha1",
	}
	if err := runOpenSSL(tmpDir, pfxArgs...); err != nil {
		fail("Ошибка при экспорте в PFX: %v", err)
		return
	}

	// Копирует сгенерированный PFX рядом с "GenCryAgent.exe"
	fmt.Println("3/3 Сохранение результата рядом с программой...")
	srcPFX := filepath.Join(tmpDir, "CryptoAgent.pfx")

	if err := copyFile(srcPFX, destPFX); err != nil {
		fail("Не удалось сохранить PFX: %v", err)
		return
	}

	fmt.Print("Готово!\n\n")
	fmt.Printf("Файл: %s\n", destPFX)
	fmt.Println("Пароль: FiReAgent")
	fmt.Println("\nНажмите Enter для выхода...")
}

// runOpenSSL выполняет команду "openssl.exe" в указанной рабочей директории (tmpDir)
func runOpenSSL(workingDir string, args ...string) error {
	cmdPath := filepath.Join(workingDir, "openssl.exe")
	cmd := exec.Command(cmdPath, args...)
	cmd.Dir = workingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// extractOpenssl распаковывает файл из встроенной файловой системы в указанный путь
func extractOpenssl(srcPath, dstPath string) error {
	f, err := embeddedFS.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	mode := os.FileMode(0644)

	// Устанавливает права на выполнение для исполняемых файлов и DLL
	if ext := filepath.Ext(toLower(dstPath)); ext == ".exe" || ext == ".dll" {
		mode = 0755
	}
	return os.WriteFile(dstPath, data, mode)
}

// copyFile копирует содержимое исходного файла в целевой файл
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// fail выводит сообщение об ошибке и ждет нажатия клавиши "Enter" перед завершением
func fail(format string, a ...any) {
	fmt.Println()
	fmt.Printf("ОШИБКА: "+format+"\n", a...)
	fmt.Println("Нажмите Enter для выхода...")
}

// waitForEnter ожидает нажатия клавиши "Enter" для продолжения
func waitForEnter() {
	_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// toLower переводит строку в нижний регистр используя простой ASCII-преобразователь
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
