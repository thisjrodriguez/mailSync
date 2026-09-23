package main

const exampleConfig = `# mailsync - configuración
#
# La estructura vive aquí; las contraseñas NO tienen por qué. Tres formas:
#
#   1. En un fichero aparte: pon ${TRABAJO_PASS} aquí y TRABAJO_PASS=... en
#      secrets.env (o .env) en este mismo directorio, con permisos 0600.
#   2. En una variable de entorno con el mismo nombre; tiene prioridad.
#   3. Sin guardarla: borra la línea password y mailsync te la pedirá al
#      arrancar, sin mostrarla al teclearla.
#
# Destino Gmail: necesitas verificación en dos pasos activada y una
# contraseña de aplicación (Cuenta de Google > Seguridad > Contraseñas de
# aplicaciones). Activa también IMAP en Gmail > Configuración > Reenvío y
# correo POP/IMAP.
#
# Comprueba la configuración sin copiar nada:  mailsync check

accounts:
  - name: trabajo

    source:
      host: mail.midominio.com
      port: 993
      user: usuario@midominio.com
      password: ${TRABAJO_PASS}
      # tls: tls        # "tls" (993, por defecto) o "starttls" (143)

    dest:
      type: gmail       # rellena imap.gmail.com:993 por ti
      user: tucuenta@gmail.com
      password: ${GMAIL_APP_PASS}

    # Una carpeta suelta se copia con el mismo nombre. Usa from/to para
    # renombrarla en el destino (en Gmail las carpetas son etiquetas).
    folders:
      - INBOX
      - from: INBOX.Sent
        to: midominio/Enviados

    interval: 5m

# Tamaño máximo del log antes de rotar, y cuántas copias antiguas conservar.
# El disco ocupado queda acotado a max_size x (keep + 1). Valores por defecto:
# log:
#   max_size: 5MB
#   keep: 3
`
