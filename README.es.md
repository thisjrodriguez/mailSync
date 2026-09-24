# mailsync

[English](README.md) · **Español**

Copia correo de un servidor IMAP a otro. No reenvía: se conecta a ambos buzones,
descarga cada mensaje en crudo y lo deposita en el destino con `APPEND`.

Esto evita el problema de los reenvíos SMTP, que los hostings bloquean y que
Gmail rechaza por SPF/DKIM/DMARC: aquí no hay una entrega nueva, solo una copia.

- El origen no se modifica: se lee con `BODY.PEEK`, así que nada se marca como leído.
- Los bytes del mensaje no se tocan, ni se parsean ni se reescriben.
- Se preservan la fecha original y los flags (`\Seen`, `\Answered`, `\Flagged`, `\Draft`).
- Es unidireccional y solo añade: borrar en el destino nunca toca el origen.
- Es idempotente: puedes ejecutarlo mil veces sin duplicar nada.

## Instalación

    go build -o mailsync .

Sale un binario único, sin dependencias en tiempo de ejecución.

## Uso

    mailsync init      crea el directorio de trabajo y un config.yaml de ejemplo
    mailsync folders   lista las carpetas del buzón de origen de cada cuenta
    mailsync check     valida la configuración y prueba el login, sin copiar nada
    mailsync run       copia en bucle, según el intervalo de cada cuenta
    mailsync once      hace una sola pasada y termina (útil para cron)
    mailsync status    muestra lo sincronizado hasta ahora
    mailsync fingerprint HOST[:PUERTO]
                       muestra el certificado que presenta un servidor, para fijarlo

Empieza siempre por `check`: la mayoría de los problemas son credenciales,
puertos o TLS, y ahí los ves en dos segundos en vez de descubrirlos por un fallo
silencioso de madrugada.

Después usa `folders` para ver los nombres reales de las carpetas antes de
escribir tu lista. Adivinarlos es como las migraciones acaban a medio copiar.

## Directorio de trabajo

Todo vive junto, para que una copia de seguridad de una carpeta lo cubra todo:

    ~/.config/mailsync/
      config.yaml     lo único que editas tú
      secrets.env     opcional: las contraseñas, fuera del config
      state.db        SQLite con la posición de cada carpeta
      mailsync.log

La ruta se resuelve en este orden: `--config`, `$MAILSYNC_HOME`,
`$XDG_CONFIG_HOME/mailsync`, `~/.config/mailsync`.

El directorio va en `0700` y los ficheros con credenciales en `0600`. Se
comprueba en cada arranque, no solo al crearlos: si los permisos están más
abiertos, mailsync se niega a arrancar.

## Configuración

> **¿No quieres escribir el YAML a mano?** Usa el
> [generador de configuración](https://thisjrodriguez.github.io/mailSync/): rellenas un
> formulario y te da el `config.yaml`. Funciona entero en tu navegador, no envía
> nada a ningún sitio y nunca te pide contraseñas.

```yaml
accounts:
  - name: trabajo

    source:
      host: mail.midominio.com
      port: 993                    # por defecto
      user: usuario@midominio.com
      password: ${TRABAJO_PASS}
      tls: tls                     # "tls" (993) o "starttls" (143)

    dest:
      type: gmail                  # rellena imap.gmail.com:993
      user: tucuenta@gmail.com
      password: ${GMAIL_APP_PASS}

    folders:
      - INBOX                      # mismo nombre en el destino
      - from: INBOX.Sent           # o renombrada
        to: midominio/Enviados

    interval: 5m
```

### Dónde poner las contraseñas

Tienes tres formas, y puedes mezclarlas entre cuentas:

**En un fichero aparte.** Pon `${TRABAJO_PASS}` en el config y el valor en
`secrets.env` —o `.env`, se aceptan los dos nombres— en el mismo directorio:

```
TRABAJO_PASS=la-contraseña
GMAIL_APP_PASS=abcd efgh ijkl mnop
```

Así el `config.yaml` no contiene secretos y puedes compartirlo o versionarlo.

**En una variable de entorno** con ese mismo nombre. Tiene prioridad sobre el
fichero, que es lo práctico para contenedores y para systemd.

**Directamente en el config.** Puedes escribirla tal cual en `password:`. Es lo
más cómodo y lo menos protegido: queda en claro en el fichero, así que déjalo en
`0600` y no lo subas a ningún repositorio.

**Sin guardarla en ningún sitio.** Quita la línea `password` y mailsync te la
pedirá al arrancar, sin mostrarla mientras la escribes:

```
Contraseña de usuario@midominio.com (trabajo, origen):
```

Se pide una sola vez, antes de la primera conexión, y se reutiliza mientras el
proceso siga vivo. Es la opción más segura porque la contraseña nunca toca el
disco, pero no sirve para `cron` ni para un servicio que arranque solo: sin
terminal, mailsync te lo dice y no arranca a medias.

### Tamaño del log

El log está acotado y rota solo, para que un demonio que no miras nunca no te
llene el disco. Por defecto son 5 MB por fichero más tres copias rotadas: unos
20 MB como mucho. Para cambiarlo:

```yaml
log:
  max_size: 5MB     # 500KB, 5MB, 1GB o un número de bytes
  keep: 3           # copias rotadas; 0 no conserva ninguna
```

La estructura va en el YAML; los secretos no tienen por qué. Cualquier
`${VARIABLE}` se sustituye desde el entorno o desde `secrets.env`, un fichero de
líneas `CLAVE=valor` en el mismo directorio. El entorno tiene prioridad. La
sustitución se hace sobre los valores, no sobre el texto: un `${...}` escrito en
un comentario se queda como está.

## Servidores con certificado inválido

Los hostings compartidos suelen servir un certificado autofirmado a nombre del
nodo, que la verificación TLS normal rechaza. En vez de desactivar la
verificación, fija el certificado:

    mailsync fingerprint mail.midominio.com:993

Comprueba que el certificado es el que esperas y añade el resumen a ese endpoint:

```yaml
    source:
      host: mail.midominio.com
      fingerprint: 3fa9c1e7b0d24856af73c9e1082b64d5ff17ae3c95b0d8427e6a1cb35d940f2e
```

Con un pin, mailsync exige una coincidencia exacta con ese certificado. Un
impostor sigue siendo rechazado, que es justo lo que no conseguirías saltándote
la verificación. Si el servidor cambia de certificado legítimamente, la conexión
falla y te dice que vuelvas a ejecutar `fingerprint`.

## Gmail como destino

Con contraseña de aplicación, sin OAuth:

1. Activa la verificación en dos pasos en tu cuenta de Google.
2. Cuenta de Google → Seguridad → Contraseñas de aplicaciones. Genera una.
3. Pégala en el config; los espacios sobran y se quitan solos.
4. Gmail → Configuración → Reenvío y correo POP/IMAP → activa IMAP.

En Gmail las carpetas son etiquetas. Un `to: midominio/Enviados` crea la
etiqueta anidada correspondiente.

## Cómo evita los duplicados

Por cada carpeta se guardan el `UIDVALIDITY` del buzón y el último UID copiado.
Cada pasada pide solo lo que está por encima de esa marca.

Si el servidor de origen cambia el `UIDVALIDITY` —renumera el buzón y deja
inservibles los UIDs guardados— mailsync vuelve a recorrer la carpeta desde
cero, pero filtra por `Message-ID` los mensajes que ya había copiado. Por eso no
acabas con el buzón duplicado tras una migración del hosting.

La marca de progreso se escribe después de que el `APPEND` haya ido bien: si el
proceso muere a mitad, como mucho se recopia un mensaje, nunca se pierde uno.

## Límites conocidos

- Sin OAuth: solo autenticación con usuario y contraseña (o contraseña de aplicación).
- Sondeo por intervalo, no IMAP IDLE. La latencia es como mucho el `interval`.
- Los mensajes de más de 60 MB se omiten y se anotan en el log.
- Las contraseñas se guardan en claro en disco, protegidas solo por permisos.

## Tests

    go test ./...

La suite levanta servidores IMAP de verdad en memoria, sobre TLS, y comprueba el
ciclo completo: que los bytes lleguen intactos, que el origen no se modifique,
que repetir pasadas no duplique, que un reinicio de UIDs no rompa nada y que un
certificado fijado se respete.
