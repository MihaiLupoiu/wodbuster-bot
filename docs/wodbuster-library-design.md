# Diseño de la librería `wodbuster` para Go

Documento de diseño de la superficie pública. **No hay implementación**: las firmas están para
razonar sobre la forma de la API, no para copiarlas y compilar.

Contexto: la librería se inyectará luego en `wodbuster-bot` (bot de Telegram multiusuario con
MongoDB, credenciales cifradas y scheduler). Ese consumidor es el que manda en las decisiones
de abajo, pero la librería no debe saber que existe.

---

## 1. Objetivos

### 1.1 Estrategia: el CLI es el banco de pruebas

El repo tiene dos binarios, `cmd/bot` (Telegram) y `cmd/script` (CLI), y esa separación es la
que ordena el trabajo: **si el CLI reserva, la librería es correcta.**

El razonamiento es que el riesgo está repartido de forma muy desigual. Reservar una clase tiene
dos clases de dificultad y **son ortogonales**:

| | Dónde vive | Cómo se prueba |
|---|---|---|
| Hablar con WodBuster: autenticar, resolver ids, ganar la carrera | la librería | un CLI y un domingo |
| Cron, Mongo, cifrado, conversación de Telegram, multiusuario | `cmd/bot` | pruebas de su propia lógica |

Probar lo segundo es caro: hay que levantar Mongo, simular la API de Telegram, esperar a que
dispare un cron. Probar lo primero solo necesita un binario y una terminal. Hoy están
entrelazados —`BookClass(ctx, email, password, day, classType, hour)` mezcla las dos cosas— y
por eso cuesta.

Si el protocolo vive en una librería que el CLI ejercita de punta a punta, los tests del bot
dejan de tener que demostrar que se sabe reservar: eso ya está demostrado. Solo tienen que
demostrar que el bot llama a la librería cuando toca y guarda el resultado. Que es
razonablemente fácil.

### 1.2 Qué es la v1

**Un binario que, lanzado entre 5 y 10 minutos antes de las 12:00 de un domingo, reserva las
clases configuradas.**

Terminado significa, en un domingo real:

1. Se lanza a las ~11:50 con credenciales por entorno y objetivos por configuración.
2. Autentica con Chrome y se queda con la sesión en memoria.
3. Sincroniza el reloj con el servidor y espera.
4. A las 12:00:00 detecta la publicación, resuelve los ids y reserva.
5. Sale con un código y un log que dicen qué pasó con cada objetivo: reservado, ya estabas,
   lista de espera, o un error tipado que explica por qué no.

Y en cualquier otro día de la semana: `-dry` y `-ahora` hacen el recorrido completo contra la
semana ya publicada sin reservar nada.

### 1.3 Qué necesita la v1 de la librería

Casi todo el núcleo, que es buena señal: la v1 no es una demo, ejercita la superficie entera.

- `browserauth.Authenticate` → `Session`
- `NewClient`, `Ping`, `ServerTime`
- `Schedule`, `Target`, `Resolve` (§3.4)
- `Book`, y `JoinWaitlist` si la clase está llena
- Los errores tipados de §6
- `race.Watch` con un solo atleta
- `wodbustertest` para poder probar sin esperar al domingo (§1.5)

### 1.4 Fuera de la v1

Nada de esto hace falta para reservar, y todo puede añadirse después sin romper la API:

- **Persistencia de sesiones.** Se autentica en cada ejecución (§5.1).
- **Multiusuario y fan-out.** La librería lo *permite* —un `*Client` por atleta, sin estado
  global— pero la v1 solo ejercita uno. No se diseña contra ello; tampoco se construye.
- **Todo lo de Telegram**: cron, Mongo, cifrado, conversación. Van después, y para entonces la
  parte difícil ya estará resuelta.
- **`Cancel`.** Está en la superficie por simetría, pero no bloquea la v1.
- **Autenticación sin navegador (`formauth`).** Chrome se queda en la v1: funciona y ya está
  probado. Quitarlo es una mejora, no un requisito (§5.2).
- Métricas, trazas, y cualquier cosa que no sea reservar.

### 1.5 La restricción que manda: una prueba real por semana

Esto no es una API que puedas probar a demanda. **Hay una sola oportunidad real cada siete
días**, y si falla, la siguiente es dentro de una semana. Eso no es un inconveniente: es la
restricción que da forma a la v1.

De ahí que tres cosas que parecen accesorias sean requisitos:

- **`-dry`**: recorrido completo —login, espera, resolución de ids— sin la última petición.
  Ejecutable cualquier día contra la semana publicada.
- **`-ahora`**: salta la espera. Separa "¿sé reservar?" de "¿sé esperar?", que son dos fallos
  distintos y conviene no descubrirlos juntos a las 12:00.
- **`wodbustertest`**: un servidor falso que publica con retraso, se queda sin plazas y devuelve
  errores. Es la única forma de probar la carrera entera —incluido perderla— sin domingos.

Con esas tres, el domingo real solo tiene que confirmar lo que ya sabes. Sin ellas, el domingo
es la primera vez que se ejecuta el camino completo, y eso es apostar.

### 1.6 No objetivos de la librería (permanentes)

- Persistencia. La librería no sabe qué es Mongo.
- Programación de tareas. No hay cron aquí dentro.
- Reintentar o reautenticar por su cuenta (§5).
- Cubrir toda la web. Solo calendario y reservas; ni pizarra, ni pagos, ni benchmarks.

---

## 2. Cómo quedan los paquetes

```
github.com/MihaiLupoiu/go-wodbuster
├── wodbuster/              núcleo — SOLO stdlib
├── wodbuster/browserauth/  autenticación con chromedp  (depende de chromedp)
├── wodbuster/race/         lógica de carrera            (depende del núcleo)
└── wodbuster/wodbustertest/ servidor falso para tests   (depende del núcleo)
```

**La decisión importante: el núcleo no importa chromedp.** Si `wodbuster` arrastra chromedp,
todo el que quiera leer un calendario se traga 40 MB de dependencias y un navegador en la
máquina. Separándolo, `go.mod` de quien solo consulta clases queda limpio, y los tests del
núcleo no necesitan Chrome.

El precio es que el consumidor tiene que elegir autenticador explícitamente. Me parece un
precio bueno: es una línea, y hace visible que autenticar es lo caro y lo frágil.

En tu repo esto entra como módulo aparte, no como `internal/`. `internal/` impide que la use
nadie más — incluido tú desde otro binario — y ya tienes dos (`cmd/bot`, `cmd/script`).

---

## 3. Modelo de dominio

### 3.1 El día es un tipo, no un `int64`

Esta es la trampa número uno de toda la API. El parámetro `ticks` son **los segundos unix de
la medianoche UTC de una fecha natural**. No es "la fecha en unix", no es un instante: es una
fecha del calendario codificada de una forma concreta. Si alguien hace
`time.Now().AddDate(0,0,1).Unix()` sale un número que parece razonable y está mal.

Por eso `ticks` no aparece nunca en la superficie pública:

```go
// Date es una fecha natural, sin hora ni zona.
type Date struct {
    Year  int
    Month time.Month
    Day   int
}

func DateOf(t time.Time) Date                    // toma la fecha en la zona de t
func NextWeekday(from time.Time, wd time.Weekday) Date  // el próximo lunes, etc.
func (d Date) Weekday() time.Weekday
func (d Date) String() string                    // "2026-08-24"
```

La conversión a `ticks` es privada. Nadie de fuera puede equivocarse porque no puede tocarla.

### 3.2 Clase y estado

```go
type ClassID int64

type Class struct {
    ID       ClassID
    Name     string        // "Wod", "Open box", "GYMaquinas" — tal cual lo da el box
    Date     Date
    Start    TimeOfDay     // 07:00
    Capacity int
    Booked   int
    State    ClassState
}

func (c Class) Free() int    { return c.Capacity - c.Booked }
func (c Class) IsFull() bool { return c.Free() <= 0 }

type TimeOfDay struct{ Hour, Minute int }
func ParseTimeOfDay(s string) (TimeOfDay, error)   // "7:00", "07:00", "07:00:00"
```

```go
type ClassState int

const (
    StateUnknown ClassState = iota
    StateBookable              // Inscribible — hay hueco
    StateWaitlistable          // Avisable — llena, solo lista de espera
    StateBooked                // Borrable — ya estás dentro
    StateNotIncluded           // NoTarifa / Excluido — tu tarifa no la cubre
)
```

El mapeo desde las cadenas de WodBuster vive dentro. **Un estado que no conozcamos se queda en
`StateUnknown` y no revienta el parseo del día entero** — si mañana inventan `Pendiente`, una
clase se queda opaca pero el resto del calendario sigue funcionando.

Guarda también la cadena original para diagnóstico, pero en un campo sin exportar accesible por
un método, para que nadie construya lógica sobre ella:

```go
func (c Class) RawState() string   // "Inscribible", para logs y para cuando algo huela raro
```

### 3.3 El día publicado

```go
type Schedule struct {
    Date      Date
    Classes   []Class
    Published bool           // false = el box aún no ha abierto ese día
    OpensIn   time.Duration  // >0 si el servidor dice cuánto falta; 0 si no lo dice
}

func (s Schedule) Find(t TimeOfDay, name string) (Class, bool)  // sin distinguir mayúsculas
func (s Schedule) At(t TimeOfDay) []Class
```

`OpensIn` viene de `SegundosHastaPublicacion`. Es **el contador oficial del servidor** y es lo
que hace que no haya que adivinar la hora de apertura. Lo expongo como `time.Duration` en vez
de como instante a propósito: es una duración relativa al momento de la respuesta, y
convertirla a hora de pared con el reloj local es justo el error que queremos evitar.

### 3.4 Las dos identidades de una clase

**Esta es la parte central de la API y conviene entenderla antes que nada.**

Una clase tiene dos identidades distintas, con vidas distintas:

| | Identidad persistente | Identidad efímera |
|---|---|---|
| Qué es | `(fecha, hora, nombre)` | `ClassID` |
| Cuándo existe | siempre | solo tras publicarse la semana |
| Cuánto dura | para siempre | esa semana |
| Quién la guarda | tu producto, en Mongo | nadie: se usa y se tira |

**El `ClassID` no se sabe de antemano y no hay forma de pedirlo antes.** Observado en Box Fire
Spain el 23/08/2026:

- Lunes 24/08: las 34 clases del día llevaban ids **consecutivos**, 35987 → 36020, en orden de
  hora. Wod 07:00 era 35988.
- Miércoles 26/08, Wod 07:00: 36056. Viernes 28/08, Wod 07:00: 36125. Es decir +68 y +69 entre
  días alternos: bloques de ~34 por jornada.
- **Lunes 31/08** (la semana siguiente, aún sin publicar): `LoadClass` devolvió
  `TipoNoClases: "NoCalendar"` y `Data: []`. No es falta de permisos — esas filas todavía no
  existían.

Parece una columna de identidad de base de datos, y las filas se crean cuando el box publica el
calendario. Que es, exactamente, el instante en el que empieza la carrera.

**Extrapolar los ids no es viable**, aunque la aritmética invite: el viernes tenía 11 bloques
horarios frente a 21 de lunes a jueves, así que el tamaño por jornada no es constante. Y aunque
lo fuera, habría que verificar el id antes de reservar — y verificar cuesta un `LoadClass`, que
es justo la petición que se quería ahorrar. No merece la pena jugarse una plaza.

De ahí el paso de **resolución**, que ocurre en el milisegundo cero:

```
publican  ──►  LoadClass(fecha)  ──►  emparejar (hora, nombre)  ──►  ClassID  ──►  Inscribir
                                              ~0 ms                         una petición
```

> **A escala de 10 atletas esto es una simplificación, no una necesidad.** Diez clientes
> sondeando por su cuenta son ~40 peticiones por segundo durante unos segundos: el servidor ni
> se entera. El explorador único sigue mereciendo la pena porque deja **una sola** resolución de
> ids y por tanto una sola versión de la verdad, no porque haga falta para aguantar la carga.

Por eso la identidad persistente es un tipo del **núcleo**, no del subpaquete `race`:

```go
// Target es una clase tal y como la nombra un humano. Estable en el tiempo,
// serializable, y lo único que tiene sentido persistir.
type Target struct {
    Date  Date
    Start TimeOfDay
    Name  string
}

func (s Schedule) Resolve(t Target) (Class, error)   // ErrClassNotFound si no está
```

Y por eso `Book` toma `ClassID` y no `Target`. Con N atletas del mismo box, **un solo
`LoadClass` resuelve los ids para todos**: los ids, el aforo y los horarios son globales del
box; lo único que varía por atleta es el `TipoEstado`. Si `Book` aceptase un `Target`, cada
atleta volvería a resolver y pagarías una petición extra por cabeza en el peor momento posible.

```
                 ┌─ Book(id, date) ── atleta A
LoadClass ──► id ─┼─ Book(id, date) ── atleta B
 (una vez)        └─ Book(id, date) ── atleta C
```

Como azúcar para lo que no es carrera —un `/reservar` manual por Telegram, un script— vale un
atajo, dejando claro que son dos viajes de ida y vuelta:

```go
// Resolve pide el día y busca la clase. Dos peticiones: no lo uses en la carrera.
func (c *Client) Resolve(ctx context.Context, t Target) (Class, error)
```

**Regla que conviene escribir en el doc.go del paquete:** un `ClassID` guardado en disco es un
bug. Solo tiene sentido dentro de la ventana entre la publicación y la reserva.

---

## 4. El cliente

```go
type Client struct{ /* … */ }

func NewClient(s Session, opts ...Option) (*Client, error)

type Option func(*config)
func WithHTTPClient(*http.Client) Option   // timeouts, transporte, proxy
func WithUserAgent(string) Option
func WithLogger(*slog.Logger) Option
```

Cuatro operaciones. Nada más:

```go
func (c *Client) Schedule(ctx context.Context, d Date) (Schedule, error)
func (c *Client) Book(ctx context.Context, id ClassID, d Date) error
func (c *Client) JoinWaitlist(ctx context.Context, id ClassID, d Date) error
func (c *Client) Cancel(ctx context.Context, id ClassID, d Date) error
func (c *Client) ServerTime(ctx context.Context) (time.Time, error)

// Ping comprueba que la sesión sigue viva con la petición más barata posible.
// Devuelve ErrSessionExpired si ya no vale. Sirve además para calentar la
// conexión TLS (ver §5.1).
func (c *Client) Ping(ctx context.Context) error
```

**Una llamada = una petición HTTP.** Sin reintentos, sin `Sleep`, sin sondeo, sin
re-autenticación. Todo eso es política y vive fuera. Si `Book` falla porque la clase se llenó,
devuelve `ErrClassFull` y se acabó; decidir si reintentar no es asunto de la librería.

El `*Client` es seguro para uso concurrente y barato de construir: uno por atleta, sin pool ni
registro global. Es lo que necesita tu scheduler cuando procesa N usuarios a la vez.

Por qué pasar `Date` además de `ClassID` en `Book`: la API real lo exige (`ticks` va en la
query). Podría guardarlo dentro de `ClassID` como un par, pero prefiero que la firma refleje lo
que el servidor pide en vez de esconderlo — y así `Book` es llamable sin haber pedido antes el
`Schedule`, que es lo que quieres cuando cacheas ids.

### 4.1 `ServerTime` y el reloj

```go
type Clock interface{ Now() time.Time }

func NewServerClock(ctx context.Context, c *Client, samples int) (Clock, error)
```

`ServerTime` lee la cabecera `Date` de la respuesta. Viene truncada al segundo, así que
`NewServerClock` toma varias muestras, se queda con la de menor latencia y compensa el sesgo.
En una medición real el reloj del cliente iba 0,6 s adelantado sobre el servidor — suficiente
para disparar antes de tiempo y perder.

Va como interfaz para que en los tests inyectes un reloj falso sin tocar red.

---

## 5. Sesión y autenticación

Decisión: **la librería nunca re-autentica sola.** Detecta que la sesión murió y lo dice.

```go
type Session struct {
    Box       string          // "firespain"
    AthleteID string          // el idu
    Cookies   []*http.Cookie
    IssuedAt  time.Time
}

func (s Session) Valid() error          // comprobación estructural, no de red
func (s Session) MarshalJSON() ([]byte, error)
func (s *Session) UnmarshalJSON([]byte) error
```

`Session` es un valor serializable y nada más. Tú lo guardas en Mongo cifrado, con tu
`encryptionKey`, con tu TTL. La librería no opina.

```go
type Authenticator interface {
    Authenticate(ctx context.Context, box string, cr Credentials) (Session, error)
}

type Credentials struct {
    Email    string
    Password string
}
```

Y la implementación con navegador, en su propio paquete:

```go
package browserauth

func New(opts ...Option) *Authenticator
func WithChromePath(string) Option
func WithHeadless(bool) Option
func WithTrustDevice(bool) Option        // la pantalla de "¿recordar este dispositivo?"
func WithDiagnosticsDir(string) Option   // vuelca HTML+PNG si el login falla
```

El flujo que espera el consumidor:

```
Session guardada ──► NewClient ──► Schedule/Book
                                      │
                                      └─ ErrSessionExpired
                                             │
                                             ▼
                              el producto decide: descifra la contraseña,
                              llama al Authenticator, guarda la nueva Session,
                              reintenta
```

**Por qué así y no con auto-refresh.** Meter un `Authenticator` dentro del `Client` significa
que las credenciales viajan por el camino crítico de cada reserva y que un re-login de 10
segundos puede dispararse en medio de la carrera de las 12:00. Además obliga a la librería a
tener una política de "cuándo re-autenticar" que no puede acertar: tú tienes rate limiting,
cifrado y estado por usuario, y ella no.

Hay un coste real: cada consumidor escribe su propio bucle de "si expira, re-autentica y
reintenta". Son unas quince líneas. Me parece mejor que esconderlas.

### 5.1 Ciclo de vida en producción: preparar antes, disparar en frío

Autenticar cuesta entre 5 y 15 segundos (arrancar Chrome, cargar el formulario, la pantalla de
dispositivo). **Eso no puede pasar a las 12:00:00.** El diseño supone que el consumidor prepara
las sesiones antes y a la hora de la verdad solo dispara HTTP.

Cronología propuesta para N atletas:

```
T-10 min  autenticar a los atletas (con 10, sobra de largo)
T-60 s    construir *Client en memoria · sincronizar reloj
T-10 s    Ping de nuevo: calienta TCP+TLS y reajusta el reloj
T-2 s     un solo cliente "explorador" empieza a sondear LoadClass
T+0       publican → ids → todos los Book en paralelo
```

**Cuatro cosas que no son obvias:**

**1. Reautenticar en cada tanda: decisión tomada, y es la correcta por ahora.** Se autentica
con usuario y contraseña en la ventana de preparación, y no se persiste ninguna cookie entre
ejecuciones. Con pocos atletas eso son unos segundos de Chrome dentro de una ventana de veinte
minutos: sobra. Guardar sesiones añade caducidad, rotación del ticket y una credencial al
portador más que cifrar y revocar, a cambio de nada que hoy duela.

**Lo único que no hay que estropear ahora**, para poder cambiar de idea gratis: que `Session`
guarde **el jar entero (`[]*http.Cookie`), no una cookie suelta**.

Es el único detalle caro de corregir después, porque se filtra al esquema de Mongo. Y hoy está
mal en `wodbuster-bot`:

```go
// internal/models/user.go — el campo a cambiar antes de que haya datos que migrar
WODBusterSessionCookie *http.Cookie `bson:"wodbuster_session_cookie,omitempty"`
//                     ^^^^^^^^^^^^ singular
```

Un login de WodBuster deja varias cookies, no una. Guardar solo la "principal" funciona hasta
que deja de funcionar, y para entonces es una migración de base de datos, no un refactor.
Cambiarlo ahora, con la colección vacía, es gratis.

Todo lo demás se puede añadir el día que haga falta sin romper nada, porque **en Go añadir un
método o un campo es retrocompatible**: persistir la sesión, un `Client.Session()` que devuelva
las cookies que el servidor haya rotado, `WithTrustDevice(true)`. No hay prisa por ninguno.

**Cuándo replantearlo.** Cuando esto se acerque a la ventana de preparación:

```
tiempo_de_login × atletas ÷ navegadores_en_paralelo
```

Con 10 s por login, 3 navegadores en paralelo y una ventana de 10 minutos, el techo está en
**unos 180 atletas**. Estás en 10, así que esto no es un problema que haya que resolver: es un
número para saber que no hace falta pensarlo. La señal que llegaría antes que el reloj son los
logins fallando por volumen; cuenta los fallos de `Authenticate` por tanda y ya está.

**2. Los navegadores hay que limitarlos, pero con 10 atletas casi da igual.** Cada Chrome
headless son 100-300 MB de RSS. El pool va con semáforo igualmente, porque es una línea y te
protege el día que crezca — pero echa cuentas antes de preocuparte:

| Atletas | Secuencial (1 Chrome) | 3 en paralelo | RAM pico |
|---|---|---|---|
| **10** | ~100 s | ~35 s | 0,3-0,9 GB |
| 50 | ~8 min | ~3 min | 0,3-0,9 GB |

Con 10, **una ventana de 5-10 minutos sobra**, que es justo lo que pide la v1 en §1.2. Y cabe
incluso secuencialmente en un micro AMD de 1 GB (§5.2): un solo Chrome vivo a la vez, 100
segundos, sin acercarse al límite. A este tamaño, el despliegue no tiene respuesta equivocada.

**3. La trampa de la conexión inactiva.** `http.Transport` cierra las conexiones ociosas a los
90 segundos por defecto. Si calientas a las 11:45 y disparas a las 12:00, la conexión ya no
existe y tu primera petición —la de la carrera— paga DNS + TCP + TLS otra vez, entre 100 y
300 ms. Dos arreglos, y conviene hacer los dos:

```go
wodbuster.WithHTTPClient(&http.Client{
    Transport: &http.Transport{
        IdleConnTimeout:     30 * time.Minute,  // que sobreviva a la espera
        MaxIdleConnsPerHost: 4,
        ForceAttemptHTTP2:   true,
    },
})
```

y además el `Ping` de T-10 s, que reabre lo que se haya caído y de paso resincroniza el reloj.

**4. Validar, no suponer.** Una sesión guardada puede estar muerta y no lo sabes hasta que la
usas. Por eso existe `Ping`: si a las 11:40 falla, tienes veinte minutos para reautenticar **y
para avisar al usuario por Telegram** de que revise su contraseña. Enterarte a las 12:00:00 es
enterarte tarde.

**Sobre los secretos.** La contraseña descifrada vive lo justo: descifrar → `Authenticate` →
soltar. No mantengas un mapa de contraseñas en claro durante veinte minutos esperando la
apertura; lo que se guarda entre medias es la `Session`, y **cifrada igual que la contraseña**,
porque una cookie de sesión es exactamente igual de sensible.

### 5.2 Chrome se queda por ahora, y dónde se despliega esto

**Decisión: la v1 autentica con chromedp.** Funciona, está probado y no bloquea nada. Quitar el
navegador es una optimización con fecha abierta, no un requisito.

#### Lo que sabemos para el día que se quite (`formauth`)

Apuntado aquí para no volver a investigarlo desde cero. El login es WebForms clásico:

```
POST https://wodbuster.com/account/login.aspx
  CSRFToken, __EVENTTARGET, __EVENTARGUMENT, __VIEWSTATE, __VIEWSTATEC, __EVENTVALIDATION
  ctl00$ctl00$body$body$CtlLogin$IoEmail
  ctl00$ctl00$body$body$CtlLogin$IoPassword
  ctl00$ctl00$body$body$CtlLogin$CtlAceptar = "Aceptar"
  ctl00$ctl00$body$body$CtlLogin$IoTri / IoTrg / IoTra   (ocultos, vacíos)
```

GET a la página, recoger los ocultos, POST. Rutinario. El riesgo está en el **segundo paso**:
los botones de dispositivo (`body_body_CtlConfiar_CtlSeguro`,
`body_body_CtlConfiar_CtlNoSeguroConfianza`) viven dentro de `body_body_CtlUp` — ese `Up` casi
seguro es un UpdatePanel — y la página carga `ScriptResource.axd` dos veces: hay ASP.NET AJAX.
Un postback parcial a mano exige mandar el campo del ScriptManager con `panel|control` y
entender la respuesta delta con pipes. Además `__VIEWSTATEC` trae 384 caracteres con
`__VIEWSTATE` vacío: viewstate comprimido.

**El experimento de veinte minutos que decide si merece la pena:** hacer login y, *antes de
pulsar nada en la pantalla de dispositivo*, abrir otra pestaña en
`firespain.wodbuster.com/athlete/reservas.aspx`. Si carga, la cookie de autenticación ya está
emitida, el segundo paso es opcional y `formauth` se reduce al POST. Si no carga, hay que
replicar el postback parcial y el coste sube mucho.

Dos incógnitas pendientes: si `/js/app.js` rellena `IoTri/IoTrg/IoTra` (solo comprobé que no
los toca ningún script *en línea*; el externo no lo he leído), y si el `CSRFToken` va
emparejado con una cookie.

Cuando exista, entra como un `Authenticator` más y se encadena:
`chain{formauth, browserauth}`. Que el respaldo grite en el log: una caída silenciosa al
navegador es una avería que no te enteras de que tienes.

#### Dónde correrlo

Dos opciones gratuitas de verdad. **Oracle encaja mejor para este caso**, sobre todo por la
región.

| | Oracle Cloud Always Free | Google Cloud e2-micro |
|---|---|---|
| CPU / RAM | 2 OCPU ARM / 12 GB · más 2 micro AMD de 1 GB | 1 e2-micro, núcleo compartido, ~1 GB |
| Disco | 200 GB | 30 GB |
| Región | la eliges al registrarte — **hay europeas** | solo `us-west1`, `us-central1`, `us-east1` |
| Latencia a WodBuster | milisegundos desde Madrid o Frankfurt | 100-150 ms desde EEUU |
| ¿Cabe Chrome? | de sobra | justo; con `formauth`, holgado |
| Tarjeta | sí | sí |
| Pegas | recortaron el ARM a la mitad en junio de 2026 sin avisar · reclaman instancias ociosas · a veces no hay capacidad en las regiones populares | 1 GB/mes de salida antes de cobrar · núcleo compartido |

La latencia es el argumento de peso: en una carrera que se decide en milisegundos, 150 ms de
ida y vuelta extra son 150 ms regalados. Los micro AMD de Oracle (1 GB) son el plan B si no hay
capacidad ARM — y ahí `formauth` pasaría de mejora a necesidad.

El tier gratis de Oracle es **ARM**, así que compila a `linux/arm64` (el `build.sh` ya lo hace).
Para el navegador, `chromium` en arm64 lleva años disponible, y desde julio de 2026 también hay
Google Chrome oficial para ARM64 Linux; `chromedp` encuentra cualquiera de los dos.

**Descartado: GitHub Actions.** Los workflows programados iban con un retraso medio de 4 h 30
min en mayo de 2026, con casos de 14 horas, y bajo carga GitHub **descarta ejecuciones sin
dejar rastro** — ni log, ni error, ni aviso. Para un disparo a las 12:00:00 es inservible.

**Redundancia gratis.** Ejecutarlo a la vez en el VPS y en un servidor de casa no da problemas:
el segundo en llegar recibe `Borrable` y la librería lo trata como éxito, no como error
(§3.2). Si el de casa se queda sin luz, el otro reserva igual.

---

## 6. Errores

Nada de comparar cadenas en castellano fuera de la librería.

```go
var (
    ErrSessionExpired = errors.New("wodbuster: session expired")
    ErrNotPublished   = errors.New("wodbuster: schedule not published yet")
    ErrClassNotFound  = errors.New("wodbuster: class not found")
    ErrClassFull      = errors.New("wodbuster: class is full")
    ErrAlreadyBooked  = errors.New("wodbuster: already booked")
    ErrNotIncluded    = errors.New("wodbuster: class not included in your plan")
    ErrQuotaExceeded  = errors.New("wodbuster: booking quota exceeded")
)

// APIError envuelve un rechazo del servidor que no encaja en los anteriores.
type APIError struct {
    Op      string   // "Book", "Schedule"
    Message string   // el ErrorMsg tal cual, en castellano
}
func (e *APIError) Error() string
```

El consumidor hace `errors.Is(err, wodbuster.ErrClassFull)`. Si aparece un rechazo nuevo, cae
en `APIError` con el mensaje original — ni se traga el error ni finge entenderlo.

`ErrQuotaExceeded` es el caso de tu tarifa: *Wod 3 ss/semana*. Cuando el cuarto intento de la
semana lo rechace WodBuster, el consumidor debe poder distinguirlo de "la clase está llena",
porque la reacción es opuesta: en un caso reintentas, en el otro paras.

> **Esto es lo que menos verificado tengo.** La forma de las respuestas (`EsCorrecto`,
> `ErrorMsg`, `NeedConfirmAdmin`) la deduje leyendo el JavaScript minificado del sitio; nunca
> he visto un cuerpo de respuesta de un `Inscribir` **fallido**. El conjunto de constantes de
> arriba es una hipótesis. Antes de fijar la API conviene provocar cada fallo a mano y anotar
> el `ErrorMsg` real. Ver §10.

---

## 7. El subpaquete `race`

Opcional, encima del núcleo, sin acceso a nada privado. Si no te gusta, lo tiras y escribes el
tuyo.

```go
package race

// Objetivo es un wodbuster.Target más la política de qué hacer si está llena.
// La identidad vive en el núcleo (ver §3.4); aquí solo se le añade comportamiento.
type Objetivo struct {
    wodbuster.Target
    Waitlist bool          // caer a lista de espera si no queda hueco
}

type Result struct {
    Objetivo Objetivo
    Class   wodbuster.Class   // vacía si nunca se publicó
    Outcome Outcome
    Err     error
    At      time.Time         // cuándo se resolvió
}

type Outcome int
const (
    OutcomeBooked Outcome = iota
    OutcomeAlreadyBooked
    OutcomeWaitlisted
    OutcomeFailed
)

type Options struct {
    Clock       wodbuster.Clock
    PollEvery   time.Duration  // 250ms
    StartBefore time.Duration  // empieza a sondear 2s antes
    Deadline    time.Duration  // deja de intentar tras 90s
    Attempts    int            // reintentos de Book si se pierde la carrera
    DryRun      bool
}

// Watch espera a que se publiquen los días y reserva. Un goroutine por objetivo.
func Watch(ctx context.Context, c *wodbuster.Client, objetivos []Objetivo, o Options) []Result

// OpensAt pregunta al servidor cuándo se publica un día.
func OpensAt(ctx context.Context, c *wodbuster.Client, d wodbuster.Date) (time.Time, error)
```

`Watch` es bloqueante y devuelve todo junto. Barajé un `<-chan Result` para ir informando por
Telegram según caen, y creo que acaba haciendo falta — pero como **añadido** posterior
(`WatchStream`), no como forma primaria: la síncrona es mucho más fácil de testear y es la que
quiere un binario de una sola pasada.

`DryRun` en la librería y no solo en el consumidor porque quieres poder ofrecer
`/reservar --simulacro` en el bot sin reimplementar la resolución de ids.

---

## 8. Tests: `wodbustertest`

```go
package wodbustertest

type Server struct {
    PublishAt time.Time      // antes de esto, los días salen sin publicar
    Classes   []wodbuster.Class
    Seats     map[wodbuster.ClassID]int
    Requests  []Request      // lo que se le pidió, para hacer aserciones
}

func NewServer(t *testing.T) *Server   // httptest por dentro; se cierra solo
func (s *Server) Session() wodbuster.Session
func (s *Server) URL() string
```

Un `httptest.Server` que habla el protocolo real, no un mock de interfaz. Con esto tu producto
testea el scheduler de punta a punta contra un WodBuster de mentira que publica con retraso, se
queda sin plazas y devuelve errores — sin generar mocks y sin tocar la red.

Tu repo tiene ahora **1644 líneas de mocks generados** en `internal/telegram`. Un servidor falso
de ~200 líneas cubre más y no miente: un mock afirma lo que tú crees que hace el cliente, el
servidor falso ejerce lo que el cliente hace de verdad.

---

## 9. Cómo se ve desde tu producto

```go
sess, err := store.LoadSession(ctx, chatID)
client, _ := wodbuster.NewClient(sess)

results := race.Watch(ctx, client, targets, race.Options{Clock: clock})

if errors.Is(firstErr(results), wodbuster.ErrSessionExpired) {
    pw, _ := manager.GetDecryptedPassword(ctx, chatID)
    sess, err = auth.Authenticate(ctx, box, wodbuster.Credentials{Email: user.Email, Password: pw})
    store.SaveSession(ctx, chatID, sess)
    // reintentar
}
```

Ese `GetDecryptedPassword` es el que hoy no llega al scheduler. Con esta forma el compilador no
te deja olvidarlo: `Authenticate` pide `Credentials` y no hay manera de pasarle una contraseña
vacía por accidente y que el fallo aparezca tres capas más abajo.

---

## 10. Lo que hay que verificar antes de fijar la API

Ordenado por lo que más cambiaría el diseño:

1. **Cuerpos de respuesta de los fallos.** Provocar a mano: reservar una clase llena, reservar
   con la tarifa agotada, reservar con la sesión caducada, reservar una clase excluida. Anotar
   el JSON exacto. De aquí sale el conjunto real de §6.
2. **Qué devuelve la sesión caducada.** ¿HTTP 302 al login? ¿200 con HTML? ¿JSON de error? La
   detección de `ErrSessionExpired` depende entero de esto.
3. **`SegundosHastaPublicacion` antes de la apertura.** Lo he visto en negativo (ya publicado).
   Falta confirmar que sale en positivo el domingo antes de las 12:00, y para qué días. Si no,
   `OpensAt` no puede existir tal cual y `race` tiene que caer al reloj de pared.
4. **Estabilidad del `idu`.** ¿Es el mismo entre sesiones? Si lo es, se puede cachear aparte de
   las cookies y `Session` se simplifica.
5. **`connectionId` vacío.** Lo dejo vacío porque leyendo el JS solo alimenta el aviso por
   SignalR. Habría que confirmar que el servidor no lo valida.
6. **`Calendario_Borrar`.** Lo he visto en el JS con la misma forma, pero no lo he ejercido.
   `Cancel` está en la API por simetría y puede que necesite un `confirm=1`.
7. **Estabilidad de los `ClassID` tras publicar.** Si el box añade o mueve una clase el lunes
   por la tarde, ¿cambian los ids de las demás? Si cambiasen, un id resuelto y cacheado durante
   la carrera podría quedar obsoleto en minutos. Lo esperable es que no, pero no lo he
   comprobado.
8. **Ámbito de los `ClassID`.** Solo he visto un box. No sé si el contador es global de
   WodBuster o por centro; afecta a si `ClassID` necesita llevar el box dentro.
9. **Límites de peticiones.** Sondear a 4/s durante unos segundos no ha dado problemas, pero no
   sé si hay rate limiting. Si lo hay, `PollEvery` necesita un mínimo.

Los puntos 1 y 2 los puedes cerrar en veinte minutos con la pestaña de red abierta.

---

## 11. Cosas que dejo fuera a propósito

- **Un `ClassType` enumerado.** Los nombres los pone cada box (`Wod`, `Open box`, `BOMBEROS`,
  `Pierna/Gluteo`…). Un enum en la librería se queda obsoleto en cuanto un box añada una clase.
  `string` y comparación sin distinguir mayúsculas.
- **Abreviaturas de día (`L`, `M`, `X`…).** Son de la UI. Dentro, `time.Weekday` y `Date`.
- **Caché de horarios.** Tentador, y veneno en una carrera: una respuesta cacheada de medio
  segundo es exactamente lo que no quieres a las 12:00:00. Si alguien la quiere, que la ponga
  fuera.
- **Métricas y trazas.** Con `WithHTTPClient` puedes envolver el `RoundTripper` y sacarlas tú.

---

## 12. Compatibilidad

Saldría como `v0` hasta cerrar §10, porque los errores de §6 van a cambiar. Cuando eso esté
verificado contra fallos reales, `v1` y ya no se toca la superficie.

Las dos cosas que romperán compatibilidad si se hacen mal desde el principio, y por eso están
así: **`Date` como tipo** (si sale `int64`, no hay vuelta atrás sin romper a todo el mundo) y
**los errores tipados** (si el consumidor acaba comparando cadenas, cualquier cambio de
WodBuster rompe su código en silencio, no el tuyo).
