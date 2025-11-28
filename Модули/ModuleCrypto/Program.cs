// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.IO;
using System.IO.Pipes;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Reflection;
using System.Linq;
using System.Security.AccessControl;
using System.Security.Principal;
using Microsoft.Win32;

// Статический импорт классов Crypto и MqttID (позволяет использовать все статические методы класса в текущем файле, без указания имени класса)
using static ModuleCrypto.Crypto;
using static ModuleCrypto.MqttID;

namespace ModuleCrypto
{
    internal class Program
    {
        private const string CurrentVersion = "26.11.25"; // Текущая версия ModuleCrypto в формате "дд.мм.гг"

        internal static void Main(string[] args)
        {
            // Показывает версию ModuleCrypto
            if (args.Length >= 1 && string.Equals(args[0], "--version", StringComparison.OrdinalIgnoreCase))
            {
                Console.WriteLine($"Версия \"ModuleCrypto\": {CurrentVersion}");
                return;
            }

            try
            {
                // Получает значение BaseTime из реестра и переводит в HEX
                string regPath = @"SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters";
                object baseTimeObj = Registry.LocalMachine.OpenSubKey(regPath)?.GetValue("BaseTime");
                if (baseTimeObj == null)
                {
                    Logging.WriteToLogFile("Не удалось получить данные из реестра.");
                    return;
                }
                string baseTimeHex = Convert.ToInt32(baseTimeObj).ToString("x8");

                // Проверка аргументов командной строки
                if (args.Length < 2)   // Минимум 2 аргумента
                {
                    Console.WriteLine("Модуль работает только в составе программы!");
                    Logging.WriteToLogFile("Модуль работает только в составе программы!");
                    return;
                }

                // Проверка первого аргумента (BaseTime) из реестр
                string baseTimeArg = args[0].Trim().ToLower();
                if (!baseTimeArg.Equals(baseTimeHex, StringComparison.OrdinalIgnoreCase))
                {
                    Console.WriteLine("Неверные данные в аргументе!");
                    Logging.WriteToLogFile("Неверные данные в аргументе!");
                    return;
                }

                // Проверка второго аргумента (режим работы "full" - все данные или "half" - только URL и сертификаты)
                string mode = args[1].Trim().ToLower();
                if (mode != "full" && mode != "half")
                {
                    Console.WriteLine("Неверный режим работы.");
                    Logging.WriteToLogFile("Неверный режим работы.");
                    return;
                }

                // Определение путей к конфигурационным файлам
                string executablePath = Path.GetDirectoryName(Assembly.GetExecutingAssembly().Location);
                string configFolder = Path.Combine(executablePath, "config");
                string certFolder = Path.Combine(executablePath, "cert");

                // Пути к PEM-файлам сертификатов
                string clientCertPem = Path.Combine(certFolder, "client-cert.pem");
                string clientKeyPem = Path.Combine(certFolder, "client-key.pem");
                string caCertPem = Path.Combine(certFolder, "server-cacert.pem");
                string clientCertEnc = Path.Combine(certFolder, "client-cert.enc");
                string clientKeyEnc = Path.Combine(certFolder, "client-key.enc");
                string caCertEnc = Path.Combine(certFolder, "server-cacert.enc");

                // Пути к зашифрованным ключам
                string aesKeyEnc = Path.Combine(configFolder, "aeskey.enc");
                string authAesKeyEnc = Path.Combine(configFolder, "auth_aeskey.enc");

                // Пути к конфигу "auth.txt" и его зашифрованной версии "auth.enc"
                string authFilePath = Path.Combine(configFolder, "auth.txt");
                string encryptedAuthFilePath = Path.Combine(configFolder, "auth.enc");

                // Поиск сертификата в хранилище Windows (для шифрования)
                X509Store store = new(StoreName.My, StoreLocation.LocalMachine);
                store.Open(OpenFlags.ReadOnly);
                X509Certificate2Collection certificates = store.Certificates.Find(
                    X509FindType.FindBySubjectName, "CryptoAgent", false);

                if (certificates.Count == 0)
                {
                    Logging.WriteToLogFile("Сертификат с CN 'CryptoAgent' не найден.");
                    return;
                }

                X509Certificate2 cert = certificates[0];
                store.Close();

                // Генерация имени канала из аргументов
                string pipeName = "FiReMQ_Crypto_Pipe";
                foreach (var arg in args)
                {
                    if (arg.StartsWith("--pipename="))
                    {
                        pipeName = arg.Substring("--pipename=".Length);
                        break;
                    }
                }

                // Проверяет наличие незашифрованных PEM-файлов
                bool plainFilesExist = File.Exists(clientCertPem) && File.Exists(clientKeyPem) && File.Exists(caCertPem);

                // Проверяет наличие зашифрованных файлов
                bool encryptedFilesExist = File.Exists(aesKeyEnc) && File.Exists(clientCertEnc) && File.Exists(clientKeyEnc) && File.Exists(caCertEnc);

                bool pipeMode = args.Any(arg => arg == "--pipe");

                // Шифрование PEM-файлов при их наличии
                if (plainFilesExist)
                {
                    HandleNormalMode(clientCertPem, clientKeyPem, caCertPem, aesKeyEnc, clientCertEnc, clientKeyEnc, caCertEnc, cert);
                    encryptedFilesExist = true;
                }

                // Переменные для хранения данных из конфига "auth.txt"
                string serverURL = null;
                string portMQTT = "8783";
                string loginMQTT = null;
                string passwordMQTT = null;
                string portQUIC = "4242";
                byte[] aesKey = null;

                // Если существует "auth.txt", использует его и перезаписывает "auth.enc"
                if (File.Exists(authFilePath))
                {
                    Console.WriteLine("Обнаружен новый файл конфига 'auth.txt'.");
                    Logging.WriteToLogFile("Обнаружен новый файл конфига 'auth.txt'.");
                    string[] lines = File.ReadAllLines(authFilePath);
                    ParseAuthContent(string.Join("\n", lines), out serverURL, out portMQTT, out loginMQTT, out passwordMQTT, out portQUIC);

                    if (string.IsNullOrEmpty(serverURL) || string.IsNullOrEmpty(portMQTT) || string.IsNullOrEmpty(loginMQTT) || string.IsNullOrEmpty(passwordMQTT))
                    {
                        Console.WriteLine("Заполните все поля в файле 'auth.txt'.");
                        Logging.WriteToLogFile("Заполните все поля в файле 'auth.txt'.");
                        return;
                    }

                    // Шифрует "auth.txt" и перезаписывает старый "auth.enc"
                    aesKey = GenerateAesKey();
                    EncryptAuthFile(authFilePath, encryptedAuthFilePath, aesKey, Path.Combine(configFolder, "auth_aeskey.enc"), cert);
                    Console.WriteLine("Файл 'auth.txt' зашифрован и сохранён.");
                    Logging.WriteToLogFile("Файл 'auth.txt' зашифрован и сохранён.");

                    // Удаляет "auth.txt"
                    if (File.Exists(authFilePath))
                    {
                        try
                        {
                            File.Delete(authFilePath);
                        }
                        catch (Exception ex)
                        {
                            Logging.WriteToLogFile($"Не удалось удалить файл 'auth.txt': {ex.Message}");
                        }
                    }
                }
                else if (File.Exists(encryptedAuthFilePath))
                {
                    // Если есть только "auth.enc"
                    if (!File.Exists(authAesKeyEnc))
                    {
                        Console.WriteLine("Файл 'auth_aeskey.enc' отсутствует. Конфигурация повреждена.");
                        Console.WriteLine("Удалите файл 'auth.enc' и создайте новый 'auth.txt' через запуск программы.");
                        Logging.WriteToLogFile("Файл 'auth_aeskey.enc' отсутствует. Конфигурация повреждена.");
                        Logging.WriteToLogFile("Удалите файл 'auth.enc' и создайте новый 'auth.txt' через запуск программы.");

                        try
                        {
                            File.Delete(encryptedAuthFilePath);
                        }
                        catch (Exception ex)
                        {
                            Logging.WriteToLogFile($"Ошибка удаления 'auth.enc': {ex.Message}");
                        }

                        // Создаёт новый, пустой "auth.txt" для пересоздания конфига
                        File.WriteAllText(authFilePath,
                            "# IP-адрес или домен сервера FiReMQ (TCP)\n" +
                            "ServerURL=\n\n" +
                            "# TCP порт MQTT брокера (mTLS)\n" +
                            "PortMQTT=8783\n\n" +
                            "# Логин для подключения к MQTT брокеру\n" +
                            "LoginMQTT=\n\n" +
                            "# Пароль для подключения к MQTT брокеру\n" +
                            "PasswordMQTT=\n\n" +
                            "# UDP порт для подключения к QUIC-серверу (mTLS)\n" +
                            "PortQUIC=4242"
                        );

                        Console.WriteLine("Создан новый файл 'auth.txt'. Заполните его и перезапустите программу.");
                        Logging.WriteToLogFile("Создан новый файл 'auth.txt'. Заполните его и перезапустите программу.");
                        return;
                    }
                    else
                    {
                        try
                        {
                            aesKey = DecryptAesKey(authAesKeyEnc, cert);
                        }
                        catch
                        {
                            // Если ключ не подходит, удаляет оба файла
                            File.Delete(encryptedAuthFilePath);
                            File.Delete(authAesKeyEnc);
                            Logging.WriteToLogFile("Обнаружена несовместимость ключей. Конфиги сброшены.");
                            return;
                        }
                    }

                    // Дешифровка только если ключ валиден
                    string authContent = DecryptAuthFile(encryptedAuthFilePath, aesKey);
                    ParseAuthContent(authContent, out serverURL, out portMQTT, out loginMQTT, out passwordMQTT, out portQUIC);
                }
                else
                {
                    // Если нет ни одного файла, создаёт пустой конфиг "auth.txt"
                    Directory.CreateDirectory(configFolder);
                    File.WriteAllText(authFilePath,
                        "# IP-адрес или домен сервера FiReMQ (TCP)\n" +
                        "ServerURL=\n\n" +
                        "# TCP порт MQTT брокера (mTLS)\n" +
                        "PortMQTT=8783\n\n" +
                        "# Логин для подключения к MQTT брокеру\n" +
                        "LoginMQTT=\n\n" +
                        "# Пароль для подключения к MQTT брокеру\n" +
                        "PasswordMQTT=\n\n" +
                        "# UDP порт для подключения к QUIC-серверу (mTLS)\n" +
                        "PortQUIC=4242"
                    );

                    Console.WriteLine("Создан пустой файл 'auth.txt' в папке 'config'. Заполните его данными!");
                    Logging.WriteToLogFile("Создан пустой файл 'auth.txt' в папке 'config'. Заполните его данными!");
                    return;
                }

                // Обрабатывает режим работы через именованный канал (Named Pipe)
                if (pipeMode && encryptedFilesExist)
                {
                    // Шифрует auth.txt, если он был создан/обновлен
                    if (!string.IsNullOrEmpty(loginMQTT) && !string.IsNullOrEmpty(passwordMQTT) && File.Exists(authFilePath))
                    {
                        // Генерирует ключ и шифрует "auth.txt"
                        aesKey ??= GenerateAesKey();
                        EncryptAuthFile(authFilePath, encryptedAuthFilePath, aesKey, authAesKeyEnc, cert);
                    }

                    // Передаёт режим (mode) в HandlePipeMode
                    HandlePipeMode(pipeName, aesKeyEnc, clientCertEnc, clientKeyEnc, caCertEnc, serverURL ?? "", portMQTT ?? "8783", loginMQTT ?? "", passwordMQTT ?? "", portQUIC ?? "4242", cert, mode);
                }
                else if (encryptedFilesExist)
                {
                    // Вызывает обычный режим, если не запрошен Pipe Mode, но файлы уже зашифрованы
                    HandleNormalMode(clientCertPem, clientKeyPem, caCertPem, aesKeyEnc, clientCertEnc, clientKeyEnc, caCertEnc, cert);
                }
                else
                {
                    Logging.WriteToLogFile("Необходимые PEM файлы отсутствуют.");
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Произошла ошибка: {ex.Message}\n{ex.StackTrace}");
            }
        }

        // HandlePipeMode реализует режим работы через именованный канал для передачи расшифрованного конфига
        private static void HandlePipeMode(string pipeName, string aesKeyEnc, string clientCertEnc, string clientKeyEnc, string caCertEnc, string serverURL, string portMQTT, string loginMQTT, string passwordMQTT, string portQUIC, X509Certificate2 cert, string mode)
        {
            try
            {
                // Проверка наличия всех необходимых зашифрованных файлов
                if (!File.Exists(aesKeyEnc) || !File.Exists(clientCertEnc) || !File.Exists(clientKeyEnc) || !File.Exists(caCertEnc))
                {
                    Console.WriteLine("Необходимые зашифрованные файлы отсутствуют.");
                    Logging.WriteToLogFile("Необходимые зашифрованные файлы отсутствуют.");
                    return;
                }

                // Загружает ID для MQTT из файла или создаёт новый
                string mqtt_id = (mode == "full") ? GetOrCreateClientId() : ""; // Генерация MQTT ID ТОЛЬКО в режиме "full"

                // Получает закрытый ключ из сертификата
                RSA privateRsa = cert.GetRSAPrivateKey();
                if (privateRsa == null)
                {
                    Console.WriteLine("Закрытый ключ недоступен.");
                    Logging.WriteToLogFile("Закрытый ключ недоступен.");
                    return;
                }

                // Расшифровывает AES-ключа
                byte[] encryptedAesKey = File.ReadAllBytes(aesKeyEnc);
                byte[] aesKey = privateRsa.Decrypt(encryptedAesKey, RSAEncryptionPadding.OaepSHA256);

                // Расшифровывает сертификаты
                string clientCert, clientKey, caCert;
                try
                {
                    clientCert = DecryptFile(clientCertEnc, aesKey);
                    clientKey = DecryptFile(clientKeyEnc, aesKey);
                    caCert = DecryptFile(caCertEnc, aesKey);
                }
                catch (Exception ex)
                {
                    Logging.WriteToLogFile($"Ошибка расшифровки сертификатов: {ex.Message}");
                    return;
                }

                // Настройка безопасности канала
                var pipeSecurity = new PipeSecurity();
                var identity = WindowsIdentity.GetCurrent();

                // Разрешает доступ текущему пользователю
                pipeSecurity.AddAccessRule(new PipeAccessRule(
                    identity.User,
                    PipeAccessRights.ReadWrite,
                    AccessControlType.Allow
                ));

                // Создание именованного канала с настройками безопасности
                using (var pipeServer = new NamedPipeServerStream(
                    pipeName,                   // Использует динамическое имя
                    PipeDirection.InOut,        // Двунаправленный режим
                    1,                          // Максимальное количество клиентов
                    PipeTransmissionMode.Byte,  // Режим передачи (байтовый)
                    PipeOptions.None,           // Дополнительные опции
                    0,                          // Размер буфера (0 - по умолчанию)
                    0,                          // Размер выходного буфера
                    pipeSecurity))              // Настройки безопасности
                {
                    try
                    {
                        pipeServer.WaitForConnection(); // Ожидает подключения клиента
                        Console.WriteLine("Канал подключен.");

                        // Передача данных через канал
                        using (var writer = new BinaryWriter(pipeServer))
                        {
                            // В режиме "full" передаёт все 8 значений (без QUIC-порта)
                            if (mode == "full")
                            {
                                WriteData(writer, serverURL);   // URL
                                WriteData(writer, portMQTT);    // TCP порт MQTT
                                WriteData(writer, loginMQTT);   // Логин
                                WriteData(writer, passwordMQTT);// Пароль
                                WriteData(writer, mqtt_id);     // ID для MQTT
                                WriteData(writer, caCert);      // CA сертификат
                                WriteData(writer, clientCert);  // Клиентский сертификат
                                WriteData(writer, clientKey);   // Клиентский ключ
                            }
                            else // В режиме "half" возвращает только URL, порт QUIC и сертификаты
                            {
                                WriteData(writer, serverURL);  // URL
                                WriteData(writer, portQUIC);   // UDP порт QUIC (только в half)
                                WriteData(writer, caCert);     // CA сертификат
                                WriteData(writer, clientCert); // Клиентский сертификат
                                WriteData(writer, clientKey);  // Клиентский ключ
                            }
                            writer.Flush(); // Немедленно отправляет данные
                        }

                        // Ожидает, пока клиент не закроет соединение, чтобы избежать обрыва канала
                        while (pipeServer.IsConnected)
                        {
                            System.Threading.Thread.Sleep(50); // Небольшая задержка для проверки состояния
                        }

                    }
                    catch (UnauthorizedAccessException ex)
                    {
                        Logging.WriteToLogFile($"Ошибка доступа: {ex.Message}");
                        File.WriteAllText("pipe_error.log", $"[{DateTime.Now}] Access error: {ex}");
                    }
                    catch (IOException ex)
                    {
                        Logging.WriteToLogFile($"Ошибка ввода-вывода: {ex.Message}");
                        File.WriteAllText("pipe_error.log", $"[{DateTime.Now}] IO error: {ex}");
                    }
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Ошибка в HandlePipeMode: {ex.Message}");
            }
        }

        // HandleNormalMode выполняет шифрование PEM-файлов при первом запуске
        private static void HandleNormalMode(string clientCertPem, string clientKeyPem, string caCertPem, string aesKeyEnc, string clientCertEnc, string clientKeyEnc, string caCertEnc, X509Certificate2 cert)
        {
            // Проверяет наличие всех необходимых PEM - файлов
            if (File.Exists(clientCertPem) && File.Exists(clientKeyPem) && File.Exists(caCertPem))
            {
                // Получает открытый ключ из сертификата
                RSA rsa = cert.GetRSAPublicKey();
                if (rsa == null)
                {
                    Logging.WriteToLogFile("Не удалось получить открытый ключ.");
                    return;
                }

                // Инициализирует AES для генерации симметричного ключа
                using Aes aes = Aes.Create();
                aes.KeySize = 256;
                aes.GenerateKey();
                byte[] aesKey = aes.Key;

                // Шифрование AES-ключа открытым ключом сертификата
                byte[] encryptedAesKey = rsa.Encrypt(aesKey, RSAEncryptionPadding.OaepSHA256);
                File.WriteAllBytes(aesKeyEnc, encryptedAesKey);

                // Шифрование файлов сертификатов
                EncryptFile(clientCertPem, clientCertEnc, aesKey);
                EncryptFile(clientKeyPem, clientKeyEnc, aesKey);
                EncryptFile(caCertPem, caCertEnc, aesKey);

                // Удаление исходных PEM-файлов после шифрования
                File.Delete(clientCertPem);
                File.Delete(clientKeyPem);
                File.Delete(caCertPem);

                Console.WriteLine("Сертификаты зашифрованы.");
                Logging.WriteToLogFile("Сертификаты зашифрованы.");
            }
            else
            {
                Console.WriteLine("Необходимые PEM файлы в папке 'cert' отсутствуют.");
                Logging.WriteToLogFile("Необходимые файлы в папке 'config' отсутствуют.");
            }
        }

        // ParseAuthContent разбирает содержимое конфиг файла "auth.txt"
        private static void ParseAuthContent(string content, out string serverURL, out string portMQTT, out string loginMQTT, out string passwordMQTT, out string portQUIC)
        {
            serverURL = "";
            portMQTT = "8783";
            loginMQTT = null;
            passwordMQTT = null;
            portQUIC = "4242";

            string[] lines = content.Split('\n');
            foreach (string raw in lines)
            {
                string line = raw.Trim();
                if (string.IsNullOrEmpty(line) || line.StartsWith("#"))
                    continue; // Пропускает пустые строки и комментарии

                int sep = line.IndexOf('=');
                string key, value;

                if (sep >= 0)
                {
                    key = line.Substring(0, sep).Trim().ToLowerInvariant();
                    value = line.Substring(sep + 1).Trim();
                }
                else
                {
                    key = line.Trim().ToLowerInvariant();
                    value = "";
                }

                switch (key)
                {
                    case "serverurl": serverURL = value; break;
                    case "portmqtt": portMQTT = string.IsNullOrEmpty(value) ? "8783" : value; break;
                    case "loginmqtt": loginMQTT = value; break;
                    case "passwordmqtt": passwordMQTT = value; break;
                    case "portquic": portQUIC = string.IsNullOrEmpty(value) ? "4242" : value; break;
                }
            }
        }
    }
}
