// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	// Crypt32 обеспечивает динамическую загрузку crypt32.dll для доступа к CryptoAPI
	crypt32                              = syscall.NewLazyDLL("crypt32.dll")
	procPFXImportCertStore               = crypt32.NewProc("PFXImportCertStore")
	procCertOpenStore                    = crypt32.NewProc("CertOpenStore")
	procCertCloseStore                   = crypt32.NewProc("CertCloseStore")
	procCertEnumCertificatesInStore      = crypt32.NewProc("CertEnumCertificatesInStore")
	procCertAddCertificateContextToStore = crypt32.NewProc("CertAddCertificateContextToStore")
	procCertFreeCertificateContext       = crypt32.NewProc("CertFreeCertificateContext")
)

const (
	// Константы для работы с хранилищем сертификатов
	CERT_STORE_PROV_SYSTEM_W        = 10         // Провайдер системного хранилища
	CERT_SYSTEM_STORE_LOCAL_MACHINE = 0x00020000 // Хранилище локального компьютера

	// Флаги импорта PFX
	PKCS12_INCLUDE_EXTENDED_PROPERTIES = 0x00000010 // Включает расширенные свойства
	CRYPT_MACHINE_KEYSET               = 0x00000020 // Размещает ключи в машинном контейнере
	// Флаг CRYPT_EXPORTABLE не указывается, чтобы ключи были НЕэкспортируемыми

	// Поведение при добавлении сертификата в store
	CERT_STORE_ADD_ALWAYS = 2 // Всегда добавляет даже если сертификат уже существует
)

// DataBlob структура-обертка для передачи байтов в WinAPI
type dataBlob struct {
	cbData uint32 // Размер буфера
	pbData *byte  // Указатель на данные
}

// ProcessOptionalCertsAuth выбирает одну из папок ("extra" или "экстра"), переносит в TEMP и обрабатывает PEM/PFX/auth.txt/AIDA64.zip
func processOptionalCertsAuth(tempDir, targetDir string) error {
	// Определяет путь к каталогу установщика
	exePath, err := os.Executable()
	if err != nil {
		return nil
	}
	exeDir := filepath.Dir(exePath)

	// Определяет возможные имена входных папок
	candidateNames := []string{"extra", "экстра"}

	// Находит существующие папки-кандидаты и проверяет их содержимое
	type cand struct {
		name  string
		path  string
		empty bool
	}
	var cands []cand
	for _, name := range candidateNames {
		p := filepath.Join(exeDir, name)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			empty, _ := isDirEmpty(p) // Ошибка чтения трактуется как несущественная
			cands = append(cands, cand{name: name, path: p, empty: empty})
		}
	}

	// Нечего обрабатывать, если папки-кандидаты отсутствуют
	if len(cands) == 0 {
		return nil
	}

	// Ищет первую непустую папку согласно заданному приоритету
	chosenIdx := -1
	for i, c := range cands {
		if !c.empty {
			chosenIdx = i
			break
		}
	}

	// Если все были пустые — формально обрабатывать нечего, но их всё равно нужно удалить (удаление происходит ниже в общем цикле)
	var dstWork string
	if chosenIdx >= 0 {
		// Перемещает папку в TEMP для последующей безопасной очистки
		dstWork = filepath.Join(tempDir, cands[chosenIdx].name)
		_ = moveDir(cands[chosenIdx].path, dstWork) // При сбое просто пропускает обработку контента
	}

	// Удаляет исходные папки, так как контент уже перемещен
	for _, c := range cands {
		_ = os.RemoveAll(c.path)
	}

	// Прекращает работу, если рабочая папка недоступна
	if dstWork == "" {
		return nil
	}
	if st, err := os.Stat(dstWork); err != nil || !st.IsDir() {
		return nil
	}

	// Показывает в CMD папку ("extra" или "экстра") с файлами
	fmt.Printf(ColorSkyBlue+"Найдена папка \"%s\"!"+ColorReset+"\n", cands[chosenIdx].name)

	// --- Обрабатывает комплект PEM файлов ---
	pemFiles := []string{"client-cert.pem", "client-key.pem", "server-cacert.pem"}
	allPEM := true
	for _, f := range pemFiles {
		if fi, err := os.Stat(filepath.Join(dstWork, f)); err != nil || fi.IsDir() {
			allPEM = false
			break
		}
	}
	if allPEM {
		destCertDir := filepath.Join(targetDir, "cert")
		_ = os.MkdirAll(destCertDir, 0o755)
		for _, f := range pemFiles {
			src := filepath.Join(dstWork, f)
			dst := filepath.Join(destCertDir, f)
			perm := os.FileMode(0o644)
			if f == "client-key.pem" {
				perm = 0o600 // Устанавливает более строгие права для приватного ключа
			}
			_ = copyFile(src, dst, perm)
		}
		fmt.Printf(ColorSkyBlue+"Сертификаты скопированы в \"%s\"..."+ColorReset+"\n", destCertDir)
	}

	// --- Обрабатывает установку PFX ---
	pfxPath := filepath.Join(dstWork, "CryptoAgent.pfx")
	if fi, err := os.Stat(pfxPath); err == nil && !fi.IsDir() {
		if err := tryInstallPFXVariants(pfxPath); err == nil {
			fmt.Println(ColorSkyBlue + "Установлен в систему \"CryptoAgent.pfx\"..." + ColorReset)
		}
	}

	// --- Обрабатывает копирование конфигурационного файла auth.txt ---
	authSrc := filepath.Join(dstWork, "auth.txt")
	if fi, err := os.Stat(authSrc); err == nil && !fi.IsDir() {
		destCfgDir := filepath.Join(targetDir, "config")
		_ = os.MkdirAll(destCfgDir, 0o755)
		_ = copyFile(authSrc, filepath.Join(destCfgDir, "auth.txt"), 0o600)
		fmt.Printf(ColorSkyBlue+"Конфиг \"auth.txt\" скопирован в \"%s\"..."+ColorReset+"\n", destCfgDir)
	}

	// Обрабатывает распаковку AIDA64.zip
	aidaZip := filepath.Join(dstWork, "AIDA64.zip")
	if fi, err := os.Stat(aidaZip); err == nil && !fi.IsDir() {
		// Путь к 7z.exe (уже распакован в tempDir)
		sevenZipExe := filepath.Join(tempDir, "7z.exe")

		// Целевая папка: ...\FiReAgent\tool\AIDA64
		destToolDir := filepath.Join(targetDir, "tool")

		// Создаёт папку tool, если её ещё нет
		_ = os.MkdirAll(destToolDir, 0o755)

		// Распаковка архива
		if err := extract7z(sevenZipExe, aidaZip, destToolDir); err == nil {
			fmt.Printf(ColorSkyBlue+"AIDA64 распакована в \"%s\"..."+ColorReset+"\n", filepath.Join(destToolDir, "AIDA64"))
		} else {
			fmt.Fprintf(os.Stderr, ColorBrightRed+"Ошибка при распаковке AIDA64.zip: %v"+ColorReset+"\n", err)
		}
	}

	return nil
}

// InstallPFXFromTargetCertDir устанавливает PFX из targetDir\cert, но пропускает установку, если внешний PFX присутствует
func installPFXFromTargetCertDir(targetDir string) error {
	certDir := filepath.Join(targetDir, "cert")
	internalPFX := filepath.Join(certDir, "CryptoAgent.pfx")

	// Не выполняет действий, если внутренний PFX отсутствует
	if fi, err := os.Stat(internalPFX); err != nil || fi.IsDir() {
		return nil
	}

	// Внешние папки имеют приоритет над внутренним PFX
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		for _, name := range []string{"extra", "экстра"} {
			if fi, err := os.Stat(filepath.Join(exeDir, name, "CryptoAgent.pfx")); err == nil && !fi.IsDir() {
				// Удаляет внутренний PFX, поскольку приоритет у внешнего
				_ = os.Remove(internalPFX)
				return nil
			}
		}
	}

	// Пытается установить PFX с известным паролем, затем без
	if err := tryInstallPFXVariants(internalPFX); err == nil {
		fmt.Println(ColorSkyBlue + "Установлен в систему \"CryptoAgent.pfx\"..." + ColorReset)
	}

	// Удаляет PFX из каталога после попытки установки
	_ = os.Remove(internalPFX)
	return nil
}

// IsDirEmpty проверяет, пуст ли указанный каталог
func isDirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// MoveDir перемещает каталог, а при неудаче (например, между томами) копирует его и удаляет источник
func moveDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Выполняет копирование, если прямое перемещение невозможно (например, на разных дисках)
	if err := copyDir(src, dst); err != nil {
		return err
	}
	_ = os.RemoveAll(src)
	return nil
}

// CopyDir рекурсивно копирует содержимое исходного каталога в целевой
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

// CopyFile копирует файл, сохраняя указанные права доступа
func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// TryInstallPFXVariants пытается импортировать PFX, используя пароль "FiReAgent", а затем пустой пароль
func tryInstallPFXVariants(pfxPath string) error {
	if err := installPFXToLocalMachineNoExport(pfxPath, "FiReAgent"); err == nil {
		return nil
	}
	if err := installPFXToLocalMachineNoExport(pfxPath, ""); err == nil {
		return nil
	}
	return errors.New("не удалось установить PFX (оба варианта пароля)")
}

// InstallPFXToLocalMachineNoExport устанавливает PFX в хранилище LocalMachine\My, запрещая экспорт приватного ключа
func installPFXToLocalMachineNoExport(pfxPath, password string) error {
	data, err := os.ReadFile(pfxPath)
	if err != nil {
		return err
	}
	var blob dataBlob
	if len(data) > 0 {
		blob.cbData = uint32(len(data))
		blob.pbData = &data[0]
	}
	passPtr, _ := syscall.UTF16PtrFromString(password)

	// Импортирует PFX во временное хранилище, размещая ключи в контейнере машины
	hImport, _, callErr := procPFXImportCertStore.Call(
		uintptr(unsafe.Pointer(&blob)),
		uintptr(unsafe.Pointer(passPtr)),
		uintptr(CRYPT_MACHINE_KEYSET|PKCS12_INCLUDE_EXTENDED_PROPERTIES),
	)
	if hImport == 0 {
		return fmt.Errorf("PFXImportCertStore: %v", callErr)
	}
	defer procCertCloseStore.Call(hImport, 0)

	// Открывает целевое системное хранилище "MY" для Local Machine
	storeName, _ := syscall.UTF16PtrFromString("MY")
	hDest, _, err2 := procCertOpenStore.Call(
		uintptr(CERT_STORE_PROV_SYSTEM_W),
		0,
		0,
		uintptr(CERT_SYSTEM_STORE_LOCAL_MACHINE),
		uintptr(unsafe.Pointer(storeName)),
	)
	if hDest == 0 {
		return fmt.Errorf("CertOpenStore: %v", err2)
	}
	defer procCertCloseStore.Call(hDest, 0)

	// Переносит все сертификаты из временного хранилища в целевое
	var prev uintptr
	var added bool
	for {
		ctx, _, _ := procCertEnumCertificatesInStore.Call(hImport, prev)
		if ctx == 0 {
			break
		}
		r, _, _ := procCertAddCertificateContextToStore.Call(hDest, ctx, uintptr(CERT_STORE_ADD_ALWAYS), 0)
		if r != 0 {
			added = true
		}
		if prev != 0 {
			procCertFreeCertificateContext.Call(prev)
		}
		prev = ctx
	}
	if prev != 0 {
		procCertFreeCertificateContext.Call(prev)
	}
	if !added {
		return errors.New("не удалось добавить сертификаты из PFX")
	}
	return nil
}
