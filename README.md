# centrifuge-go-mock-client

# Что умеет
- при запуске поднимает web server и ждёт входящие запросы на добавление клиенского подключения '/connection.add'
- после запроса создает подключение к Centrifugo
- принимает и просто логирует клиентские нотифайки 

# Настройки
В файле настроек settings.yaml можно указать параметры (порядок важен):

log-filename: 	'log_file.log' 					# Имя лог-файла
http-addr: 	':8080'						# Адрес (порт) сервиса http
centrifugo-url: 'http://localhost/Centrifugo'	# Адрес центрифуги по умолчанию (можно также указать в запросе)

# Запуск
1. Запускаем центрифугу
2. Запускаем мок центрифуги:
Проверяем настройки (settings.yaml) и если всё верно, то запускаем .exe

# Добавление клиентов через http запрос
Для добавления клиентского подключения нужно отправить HTTP POST запрос на 'http://localhost:8080/connection.add' вида:

POST /connection.add HTTP/1.1
Host: localhost:8080
Cookie: "..."
Content-Type: application/json
Content-Length: ___
{
    "id": "fc8362f9-deec-48f2-b693-2666d79c71ed",
    "centrifugoUrl": "http://localhost/Centrifugo",
    // "cookie": - или тут, или в заголовке 'Cookie'
}

Для просмотра количества подключений нужно отправить HTTP GET запрос на 'http://localhost:8080/connection.count'

(!) 'id' - ClientId, Ид клиентской сессии Sungero (гуляет в заголовках запросов от клиента)

(!) 'centrifugoUrl' - адрес для подключения к centrifugoUrl (в IIS должно быть настроено url-Rewrite аналогично '/Preview' или '/Storage')
(!) 'id' - ClientId, Ид клиентской сессии Sungero (гуляет в заголовках запросов от клиента)
(!) Cookie можно прокинуть в заголовках или в теле POST запроса ("cookie": ".AspNet.Cookies=___;")

(!!!) Если ответ 200 OK, то клиентское подключение добавлено или уже существует. Если нет, то код 500, и ошибка с причиной в логах и в ответе на запрос.

(!) Узнать, подключился ли на самом деле клиент можно по количеству подключений (по запросу count или в логах). 
Total - количество добавленных, Connected - количество подключённых клиентов.
Subscribed - количество клиентов, которые подписались на события. Может отображаться неправильно при включённой настройке 'user_subscribe_to_personal' в центрифуге.

# Запуск через docker
- Запуск центрифуги:
docker run --ulimit nofile=65536:65536 -v ~/centrifugo -p 8000:8000 centrifugo/centrifugo centrifugo -c config.json
- Сборка образа:
docker build . -t centrifuge-go-mock-client
- Запуск образа:
docker run --rm --network="host" centrifuge-go-mock-client

# Запросы curl
- Узнать количество подключённых:
curl http://localhost:8080/connection.count
- Добавить подключение:
curl --data "{\"id\":\"userid\",\"centrifugoUrl\":\"http://localhost:8000\",\"cookie\":\"cookie-data\"}" http://localhost:8080/connection.add
- Удалить подключение:
curl --data "{\"id\":\"userid\"}" http://localhost:8080/connection.remove