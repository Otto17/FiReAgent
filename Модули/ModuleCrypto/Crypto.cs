// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.IO;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace ModuleCrypto
{
    internal class Crypto
    {
        // EncryptAuthFile шифрует конфиг файл "auth.txt" с использованием AES и сохраняет зашифрованный ключ
        internal static void EncryptAuthFile(string authFilePath, string encryptedAuthFilePath, byte[] key, string authAesKeyEnc, X509Certificate2 cert)
        {
            try
            {
                string content = File.ReadAllText(authFilePath);
                byte[] iv = GenerateIV();
                using (Aes aes = Aes.Create())
                {
                    aes.Key = key;
                    aes.IV = iv;
                    using (FileStream fsOutput = new(encryptedAuthFilePath, FileMode.Create, FileAccess.Write))
                    {
                        fsOutput.Write(iv, 0, iv.Length);   // Записывает IV в начало файла, чтобы дешифровать его позже
                        using (CryptoStream cs = new(fsOutput, aes.CreateEncryptor(), CryptoStreamMode.Write))
                        {
                            using (StreamWriter sw = new(cs))
                            {
                                sw.Write(content);
                            }
                        }
                    }
                }

                // Шифрует и сохраняет AES-ключ, используя публичный ключ сертификата
                byte[] encryptedAesKey = EncryptAesKey(key, cert);
                File.WriteAllBytes(authAesKeyEnc, encryptedAesKey);

                // Удаляет исходный файл, так как он больше не нужен
                try
                {
                    File.Delete(authFilePath);
                }
                catch (Exception ex)
                {
                    Logging.WriteToLogFile($"Не удалось удалить файл 'auth.txt': {ex.Message}");
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Ошибка при шифровании 'auth.txt': {ex.Message}");
            }
        }

        // DecryptAuthFile расшифровывает файл "auth.enc" используя предоставленный AES-ключ
        internal static string DecryptAuthFile(string encryptedAuthFilePath, byte[] key)
        {
            using (FileStream fsInput = new(encryptedAuthFilePath, FileMode.Open, FileAccess.Read))
            {
                byte[] iv = new byte[16];
                fsInput.Read(iv, 0, 16);    // Читает IV из начала файла
                using (Aes aes = Aes.Create())
                {
                    aes.Key = key;
                    aes.IV = iv;
                    using (CryptoStream cs = new(fsInput, aes.CreateDecryptor(), CryptoStreamMode.Read))
                    using (StreamReader reader = new(cs))
                    {
                        return reader.ReadToEnd();
                    }
                }
            }
        }

        // GenerateAesKey генерирует случайный 256-битный AES-ключ
        internal static byte[] GenerateAesKey()
        {
            using (Aes aes = Aes.Create())
            {
                aes.KeySize = 256;
                aes.GenerateKey();
                return aes.Key;
            }
        }

        // EncryptAesKey шифрует симметричный ключ, используя открытый ключ сертификата
        private static byte[] EncryptAesKey(byte[] aesKey, X509Certificate2 cert)
        {
            RSA rsa = cert.GetRSAPublicKey();
            return rsa.Encrypt(aesKey, RSAEncryptionPadding.OaepSHA256);
        }

        // DecryptAesKey расшифровывает AES-ключ, используя закрытый ключ сертификата
        internal static byte[] DecryptAesKey(string aesKeyEnc, X509Certificate2 cert)
        {
            try
            {
                if (!File.Exists(aesKeyEnc))
                {
                    Logging.WriteToLogFile("Файл 'auth_aeskey.enc' отсутствует. Создайте новый конфиг 'auth.txt'.");
                    return null;
                }

                // Получает закрытый ключ, который необходим для дешифровки
                RSA privateRsa = cert.GetRSAPrivateKey() ?? throw new InvalidOperationException("Закрытый ключ недоступен.");
                byte[] encryptedAesKey = File.ReadAllBytes(aesKeyEnc);
                return privateRsa.Decrypt(encryptedAesKey, RSAEncryptionPadding.OaepSHA256);
            }
            catch (CryptographicException)
            {
                Logging.WriteToLogFile("Ошибка расшифровки ключа: данные повреждены или сертификат неверен.");
                throw;  // Перебрасывает исключение, потому что без ключа работа невозможна
            }
            catch (IOException ex)
            {
                Logging.WriteToLogFile($"Ошибка чтения файла {aesKeyEnc}: {ex.Message}");
                throw;
            }
        }

        // WriteData записывает строку в BinaryWriter, предваряя (ставя впереди) её длиной
        internal static void WriteData(BinaryWriter writer, string data)
        {
            byte[] bytes = System.Text.Encoding.UTF8.GetBytes(data);
            writer.Write(bytes.Length);
            writer.Write(bytes);
        }

        // EncryptFile шифрует файл с использованием AES-256
        internal static void EncryptFile(string inputFile, string outputFile, byte[] key)
        {
            byte[] iv = GenerateIV();
            using (Aes aes = Aes.Create())
            {
                aes.Key = key;
                aes.IV = iv;
                using (FileStream fsInput = new(inputFile, FileMode.Open, FileAccess.Read))
                using (FileStream fsOutput = new(outputFile, FileMode.Create, FileAccess.Write))
                {
                    fsOutput.Write(iv, 0, iv.Length); // Сохраняет IV в начале файла
                    using (CryptoStream cs = new(fsOutput, aes.CreateEncryptor(), CryptoStreamMode.Write))
                    {
                        fsInput.CopyTo(cs);
                    }
                }
            }
        }

        // DecryptFile расшифровывает файл с использованием AES-256 и возвращает его содержимое как строку
        internal static string DecryptFile(string inputFile, byte[] key)
        {
            try
            {
                using (FileStream fsInput = new(inputFile, FileMode.Open, FileAccess.Read))
                {
                    byte[] iv = new byte[16];
                    fsInput.Read(iv, 0, 16); // Читает IV, который необходим для начала дешифрования

                    using (Aes aes = Aes.Create())
                    {
                        aes.Key = key;
                        aes.IV = iv;

                        using (CryptoStream cs = new(fsInput, aes.CreateDecryptor(), CryptoStreamMode.Read))
                        using (StreamReader reader = new(cs))
                        {
                            return reader.ReadToEnd();
                        }
                    }
                }
            }
            catch (CryptographicException)
            {
                Logging.WriteToLogFile($"Ошибка расшифровки файла {inputFile}: данные повреждены или ключ неверен.");
                throw; // Перебрасывает исключение, чтобы вызывающий код мог обработать сбой
            }
            catch (IOException ex)
            {
                Logging.WriteToLogFile($"Ошибка чтения файла {inputFile}: {ex.Message}");
                throw;
            }
        }

        // GenerateIV генерирует случайный вектор инициализации (IV) для AES-256
        private static byte[] GenerateIV()
        {
            using (Aes aes = Aes.Create())
            {
                aes.GenerateIV();
                return aes.IV;
            }
        }
    }
}
