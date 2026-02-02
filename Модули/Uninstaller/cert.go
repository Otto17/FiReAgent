// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	crypt32                            = syscall.NewLazyDLL("crypt32.dll")                 // Ленивая загрузка DLL crypt32
	procCertOpenStore                  = crypt32.NewProc("CertOpenStore")                  // Открывает хранилище сертификатов
	procCertFindCertificateInStore     = crypt32.NewProc("CertFindCertificateInStore")     // Ищет сертификат в хранилище
	procCertDeleteCertificateFromStore = crypt32.NewProc("CertDeleteCertificateFromStore") // Удаляет сертификат из хранилища
	procCertCloseStore                 = crypt32.NewProc("CertCloseStore")                 // Закрывает хранилище сертификатов
)

const (
	X509_ASN_ENCODING               = 0x00000001 // Кодировка X509 ASN
	PKCS_7_ASN_ENCODING             = 0x00010000 // Кодировка PKCS 7 ASN
	CERT_FIND_SUBJECT_STR_W         = 0x00080007 // Указывает на поиск по строке темы (Subject String)
	CERT_STORE_PROV_SYSTEM_W        = 10         // Провайдер системного хранилища
	CERT_SYSTEM_STORE_LOCAL_MACHINE = 0x00020000 // Хранилище локальной машины
)

var (
	ERROR_ACCESS_DENIED = syscall.Errno(5) // Ошибка отказа в доступе
)

// openLocalMachineMyStore открывает хранилище "MY" для LocalMachine
func openLocalMachineMyStore() (uintptr, error) {
	storeName, _ := syscall.UTF16PtrFromString("MY")

	hStore, _, err := procCertOpenStore.Call(
		uintptr(CERT_STORE_PROV_SYSTEM_W),
		0,
		0,
		uintptr(CERT_SYSTEM_STORE_LOCAL_MACHINE),
		uintptr(unsafe.Pointer(storeName)),
	)
	if hStore == 0 {
		return 0, fmt.Errorf("CertOpenStore error: %v", err)
	}
	return hStore, nil
}

// removeCryptoAgentCert удаляет сертификат с CN="CryptoAgent" из хранилища LocalMachine\My
func removeCryptoAgentCert() {
	hStore, err := openLocalMachineMyStore()
	if err != nil {
		fmt.Println(`Сертификат "CryptoAgent" не найден (пропускаем).`)
		return
	}
	defer procCertCloseStore.Call(hStore, 0)

	target, _ := syscall.UTF16PtrFromString("CryptoAgent")

	ctx, _, _ := procCertFindCertificateInStore.Call(
		hStore,
		X509_ASN_ENCODING|PKCS_7_ASN_ENCODING,
		0,
		CERT_FIND_SUBJECT_STR_W,
		uintptr(unsafe.Pointer(target)),
		0,
	)
	if ctx == 0 {
		fmt.Println(`Сертификат "CryptoAgent" не найден (пропускаем).`)
		return
	}

	r1, _, _ := procCertDeleteCertificateFromStore.Call(ctx)
	if r1 == 0 {
		lastErr := syscall.GetLastError()
		// Проверяет известные коды ошибок отказа в доступе (0x5 и 0x80070005), поскольку удаление может требовать высоких привилегий
		if lastErr == ERROR_ACCESS_DENIED || lastErr == syscall.Errno(0x80070005) {
			fmt.Println(`Не достаточно прав для удаления сертификата "CryptoAgent".`)
		} else {
			fmt.Println(`Сертификат "CryptoAgent" не найден (пропускаем).`)
		}
		return
	}
}
