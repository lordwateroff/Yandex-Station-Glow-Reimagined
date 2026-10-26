# Пошаговая установка

Гайд проводит от рутованной станции до работающей подсветки с веб-панелью. Всё делается с компьютера
в той же Wi-Fi сети, что и станция.

## 0. Что нужно

- **Яндекс Станция 2 с рутом.** Рут делается по вики
  [yandex-station-extended](https://github.com/Marker284/yandex-station-extended) ([hackalice.me](https://hackalice.me)).
  Сначала пройдите её до конца: должен работать `adb` или SSH до станции.
- **IP-адрес станции** в домашней сети, например `192.168.0.50`. Его видно в роутере, в списке клиентов.
  Дальше в гайде он обозначен как `<IP>`.
- **Go 1.27 или новее** на компьютере: [go.dev/dl](https://go.dev/dl/).
- **adb** ([Android Platform Tools](https://developer.android.com/tools/releases/platform-tools)) или SSH-клиент.

## 1. Скачать исходники

```sh
git clone https://github.com/lordwateroff/Yandex-Station-Glow-Reimagined.git
cd Yandex-Station-Glow-Reimagined
```

Можно и без git: кнопка **Code → Download ZIP** на GitHub, потом распаковать архив.

## 2. Проверить и собрать

Сначала тесты, они запускаются прямо на компьютере:

```sh
go test ./...
```

Потом сборка под процессор станции (Linux, arm64).

Linux, macOS, Git Bash:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o build/glow .
```

Windows PowerShell:

```powershell
$env:GOOS="linux"; $env:GOARCH="arm64"; $env:CGO_ENABLED="0"
go build -buildvcs=false -trimpath -ldflags="-s -w" -o build/glow .
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

Получится файл `build/glow` размером около 6 МБ.

## 3. Подключиться к станции

```sh
adb connect <IP>:5555
adb devices
```

В списке должна появиться станция со статусом `device`. Если подключаетесь по SSH, а не через adb,
файлы ниже копируются через `scp` в те же пути, а команды запускаются в `ssh`.

## 4. Залить программу и автозапуск

```sh
adb shell mkdir -p /data/opt/glow
adb push build/glow /data/opt/glow/glow
adb push deploy/S99zglow /etc/init.d/S99zglow
adb shell chmod +x /data/opt/glow/glow /etc/init.d/S99zglow
```

> Если `adb push` в `/etc/init.d` пишет `Read-only file system`, значит, корень смонтирован только для
> чтения. Как сделать его записываемым на вашей прошивке, смотрите в вики yandex-station-extended.

## 5. Запустить

```sh
adb shell /etc/init.d/S99zglow start
adb shell tail -n 5 /tmp/glow.log
```

В логе должна быть строка `glow: web panel on :15321`. Панель станции загорится эффектом «Сияние».

## 6. Открыть веб-панель

В браузере на компьютере или телефоне откройте `http://<IP>:15321`.

![Веб-панель](img/panel-desktop.png)

- **Свет включён:** главный выключатель. Когда он выключен, панель плавно гаснет и показывает обычный экран станции.
- **Эффект:** две вкладки, «Спокойный свет» и «Под музыку». У каждого эффекта есть живое превью.
- **Цвета:** готовые палитры и «Своя», в которой можно задать от 2 до 5 своих цветов.
- **Яркость** и **Скорость:** скорость меняется от ×0.25 до ×4, середина ползунка соответствует ×1.
- **Уступать Алисе:** пока Алиса слушает или отвечает, панель показывает её собственную анимацию,
  а потом подсветка плавно возвращается.

Настройки сохраняются сами и переживают перезагрузку.

## 7. Подсветка под музыку

![Эффекты под музыку](img/panel-music.png)

Включите музыку на станции и выберите вкладку «Под музыку»: Спектр, Пульс, Зеркало, Кольца, Голос или Пики.
Звук берётся с обратного канала динамика (`hw:0,1`), поэтому реагирует на всё, что играет станция,
в том числе на AirPlay.

Без музыки превью этих эффектов почти тёмные, так и должно быть.

## 8. Телефон

Панель работает и с телефона: в браузере на том же Wi-Fi откройте `http://<IP>:15321`. Можно добавить
её на главный экран как закладку.

<img src="img/panel-mobile.png" width="320" alt="Панель на телефоне">

## 9. Необязательно: автозапуск yamd для AirPlay

Шаг нужен, только если у вас стоит YAM с плагином station-airplay (`/data/quazar/yam`):

```sh
adb push deploy/S99zyam /etc/init.d/S99zyam
adb shell chmod +x /etc/init.d/S99zyam
adb shell /etc/init.d/S99zyam start
```

## Обновление

```sh
adb push build/glow /tmp/glow.new
adb shell "mv /tmp/glow.new /data/opt/glow/glow && chmod +x /data/opt/glow/glow && /etc/init.d/S99zglow restart"
```

## Если что-то не так

| что видно | что сделать |
|---|---|
| Панель не загорелась | `adb shell tail -n 30 /tmp/glow.log`. Если там `open /dev/fb0: ...`, значит, нет доступа к `/dev/fb0`. Если стоит плагин `ledart`, остановите его: два рисовальщика на одной панели будут мешать друг другу |
| Сайт не открывается | Проверьте, что процесс жив: `adb shell pidof glow`. Убедитесь, что телефон в той же сети, а адрес набран с `:15321` |
| Эффекты под музыку не реагируют | Посмотрите в логе строку `loopback tap`. Если её нет, музыкальный вход отключён флагом `-no-audio` |
| Алиса стала хуже слышать во время музыки | Остановите glow: `/etc/init.d/S99zglow stop`, он сам вернёт штатный `arecord`. Если после этого стало лучше, напишите в Issues |
| Подсветка не уходит, когда говорит Алиса | Включите «Уступать Алисе». Признак работы — строки `firmware panel busy=` в логе |

## Удаление

```sh
adb shell /etc/init.d/S99zglow stop
adb shell rm /etc/init.d/S99zglow
adb shell rm -r /data/opt/glow
```

При остановке glow сам возвращает штатный `arecord` на `hw:0,1`. После перезагрузки станция снова
работает как раньше.
