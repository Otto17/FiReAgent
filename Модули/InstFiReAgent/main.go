// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// Данные для "Удаление или изменение программы" (Установленные приложения)
	publisher      = "Otto"     // Автор
	CurrentVersion = "02.02.26" // Текущая версия InstFiReAgent в формате "дд.мм.гг"
)

func main() {
	initColors() // initColors Инициализирует цвета консоли (включает поддержку на Windows 10+)

	// Проверяет версию Windows (минимум 8.1)
	if !isWindows81OrGreater() {
		fmt.Fprintln(os.Stderr, ColorBrightRed+"Установка возможна начиная с Windows 8.1 и выше!"+ColorReset)
		os.Exit(1)
	}

	// Показывает справку
	if len(os.Args) >= 2 && (os.Args[1] == "?" || strings.EqualFold(os.Args[1], "-h") || strings.EqualFold(os.Args[1], "--help")) {
		printHelp()
		return
	}

	// Отображает версию InstFiReAgent
	if len(os.Args) >= 2 && strings.EqualFold(os.Args[1], "--version") {
		fmt.Printf("Версия \"InstFiReAgent\": %s\n", CurrentVersion)
		return
	}

	// Проверяет, что все переданные аргументы являются допустимыми флагами
	for _, arg := range os.Args[1:] {
		if !strings.EqualFold(arg, "--force") && !strings.EqualFold(arg, "--del") {
			fmt.Printf(ColorBrightRed+"Ошибка: Неизвестный ключ запуска \"%s\""+ColorReset+"\n", arg)
			printHelp()
			os.Exit(1)
		}
	}

	force := hasForceFlag() // force Проверяет наличие ключа --force
	del := hasDelFlag()     // del Проверяет наличие ключа --del

	fmt.Println(ColorBrightYellow + "Автор " + publisher + " (ver." + CurrentVersion + ")" + ColorReset)
	fmt.Println(ColorTeal + "Cсылка на проект: https://gitflic.ru/project/otto/fireagent" + ColorReset + "\n\n")

	// Определяет целевые каталоги установки
	targetDir := `C:\Program Files\FiReAgent`
	defFolder := `C:\ProgramData\FiReAgent`

	// Подготавливает временную папку
	tempDir, err := makeTempWorkDir()
	if err != nil {
		fail("Не удалось создать временную папку:", err)
	}
	defer os.RemoveAll(tempDir) // Обязательное удаление временной папки при выходе

	// Записывает 7z.exe и архив во временную папку
	fmt.Println(ColorPink + "Распаковка..." + ColorReset)
	sevenZipPath, err := writeEmbeddedToFile("tool/7z.exe", filepath.Join(tempDir, "7z.exe"))
	_, _ = writeEmbeddedToFile("tool/7z.dll", filepath.Join(tempDir, "7z.dll"))

	if err != nil {
		fail("Не удалось подготовить 7z.exe:", err)
	}
	archivePath, err := writeEmbeddedToFile("tool/installation.7z", filepath.Join(tempDir, "installation.7z"))
	if err != nil {
		fail("Не удалось подготовить архив:", err)
	}

	// Создаёт целевые папки
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		fail("Не удалось создать папку назначения:", err)
	}
	if err := os.MkdirAll(defFolder, 0o755); err != nil {
		fail("Не удалось создать папку ProgramData:", err)
	}

	// Применяет права полного доступа ACL
	_ = ensureAclFullControl(targetDir)
	_ = ensureAclFullControl(defFolder)

	// Добавляет пути в исключения Защитника Windows
	fmt.Printf(ColorBrightGreen+"Добавление \"%s\" в исключение Защитника Windows..."+ColorReset+"\n", targetDir)
	if err := EnsureDefenderExclusion(targetDir); err != nil {
		fmt.Fprintf(os.Stderr, ColorBrightYellow+"Предупреждение: исключение для \"%s\" не добавлено (%s)."+ColorReset+"\n", targetDir, err)
	}

	fmt.Printf(ColorBrightGreen+"Добавление \"%s\" в исключение Защитника Windows..."+ColorReset+"\n", defFolder)
	if err := EnsureDefenderExclusion(defFolder); err != nil {
		fmt.Fprintf(os.Stderr, ColorBrightYellow+"Предупреждение: исключение для \"%s\" не добавлено (%s)."+ColorReset+"\n", defFolder, err)
	}

	// Распаковывает архив в целевую папку
	fmt.Printf(ColorBrightPurple+"Установка FiReAgent в \"%s\"..."+ColorReset+"\n", targetDir)
	if err := extract7z(sevenZipPath, archivePath, targetDir); err != nil {
		fail("Ошибка распаковки:", err)
	}

	// Разворачивает содержимое, если архив создал единственную корневую папку
	if err := flattenSingleTopDir(targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "Предупреждение: не удалось развернуть структуру: %v\n", err)
	}

	// Устанавливает PFX-сертификат из папки cert
	_ = installPFXFromTargetCertDir(targetDir)

	// Обрабатывает дополнительные сертификаты и конфигурацию из папки extra
	_ = processOptionalCertsAuth(tempDir, targetDir)

	// Регистрирует приложение в разделе "Программы и компоненты"
	fmt.Println(ColorTeal + "Регистрация в системе..." + ColorReset)
	if err := registerUninstallEntry("FiReAgent", targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "Предупреждение: не удалось зарегистрировать в 'Программы и компоненты': %v\n", err)
	}

	// Запускает FiReAgent.exe с ключом -is для установки и старта службы
	fmt.Println(ColorOrange + "Запуск службы..." + ColorReset)
	fiReAgentExe := filepath.Join(targetDir, "FiReAgent.exe")
	cmd := exec.Command(fiReAgentExe, "-is")
	cmd.Dir = targetDir

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, ColorBrightRed+"Предупреждение: не удалось запустить службу:", err, ColorReset)
	}

	// Выводит результат работы службы
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) != "" {
			fmt.Println(ColorOrange + strings.TrimSpace(line) + ColorReset)
		}
	}

	// Удаляет временные файлы
	// Попытка удалить папку сразу
	removeTempErr := os.RemoveAll(tempDir)

	// Если удаление папки не удалось ИЛИ включен флаг --del, запускает отложенную задачу
	if removeTempErr != nil || del {
		if removeTempErr != nil {
			fmt.Fprintf(os.Stderr, ColorBrightYellow+
				"Не удалось удалить временную папку напрямую (%v). Удаление произойдёт после завершения установки...\n"+ColorReset, removeTempErr)
		}
		if del {
			fmt.Println(ColorPink + "Активировано самоудаление установщика..." + ColorReset)
		}
		// Запуск процесса, который дождется выхода этой программы и удалит файлы
		scheduleCleanup(tempDir, del)
	}

	fmt.Println(ColorBrightYellow + "Установка успешно завершена!" + ColorReset)

	// Ждёт нажатия Enter, если ключ --force не был передан
	if !force {
		fmt.Println("\n\n" + ColorBrightWhite + "Для выхода нажмите Enter." + ColorReset)
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
	}
}

// fail выводит ошибки в консоль и завершает программу
func fail(msg string, err error) {
	fmt.Fprintln(os.Stderr, ColorBrightRed+msg, err, ColorReset)
	os.Exit(1)
}

// genID генерирует ID заданной длины, используя символы [0-9A-Z]
func genID(n int) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[idx.Int64()])
	}
	return b.String(), nil
}

// makeTempWorkDir создаёт временную рабочую директорию в %TEMP%
func makeTempWorkDir() (string, error) {
	base := os.TempDir()
	for i := 0; i < 10; i++ {
		suffix, err := genID(5)
		if err != nil {
			return "", err
		}
		name := fmt.Sprintf("FRA_%s.tmp", suffix)
		dir := filepath.Join(base, name)
		// Проверяет, что папка действительно новая, а не существует случайно
		if err := os.Mkdir(dir, 0o755); err != nil {
			if os.IsExist(err) {
				continue
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				continue
			}
			return "", err
		}
		return dir, nil
	}
	return "", fmt.Errorf("не удалось подобрать имя для временной папки")
}

// writeEmbeddedToFile записывает содержимое встроенного файла в файловую систему
func writeEmbeddedToFile(embeddedPath, outPath string) (string, error) {
	data, err := content.ReadFile(embeddedPath)
	if err != nil {
		return "", fmt.Errorf("read embedded %s: %w", embeddedPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(outPath), err)
	}
	// Устанавливает права 0o700, чтобы не провоцировать антивирусы
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", outPath, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write %s: %w", outPath, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", outPath, err)
	}
	return outPath, nil
}

// hasForceFlag возвращает истину, если в аргументах передан ключ --force
func hasForceFlag() bool {
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, "--force") {
			return true
		}
	}
	return false
}

// hasDelFlag возвращает истину, если в аргументах передан ключ --del
func hasDelFlag() bool {
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, "--del") {
			return true
		}
	}
	return false
}

// printHelp выводит справку по доступным ключам запуска
func printHelp() {
	// Ярко белый цвет ключей, для контрастности
	blue := ColorBrightBlue
	reset := ColorReset

	fmt.Println("Доступные ключи установщика InstFiReAgent:")
	fmt.Printf("    %s?%s, %s-h%s, %s--help%s          — Вызов справки.\n", blue, reset, blue, reset, blue, reset)
	fmt.Printf("    %s--version%s              — Узнать версию установщика.\n", blue, reset)
	fmt.Printf("    %s--force%s                — Автоматическая установка, без ожидания нажатия Enter в конце.\n", blue, reset)
	fmt.Printf("    %s--del%s                  — Удаляет сам установщик InstFiReAgent, после окончания его работы.\n", blue, reset)
}
