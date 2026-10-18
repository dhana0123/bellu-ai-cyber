#!/bin/sh
# Select the nginx config at container start based on whether a TLS certificate
# exists. This lets the SAME image boot cleanly both before a cert is issued
# (HTTP only, so you can run certbot) and after (HTTPS with redirect). A
# hard-coded ssl_certificate pointing at a missing file would make nginx refuse
# to start, so we choose the config dynamically instead.
set -eu

CERT="/etc/letsencrypt/live/dev.belluai.com/fullchain.pem"
DEST="/etc/nginx/conf.d/default.conf"

if [ -f "$CERT" ]; then
    echo "frontend-entrypoint: TLS certificate found; serving HTTPS"
    cp /etc/nginx/templates/nginx.https.conf "$DEST"
else
    echo "frontend-entrypoint: no TLS certificate at $CERT; serving HTTP only"
    echo "frontend-entrypoint: obtain one with certbot (webroot /var/www/certbot), then restart this container"
    cp /etc/nginx/templates/nginx.http.conf "$DEST"
fi

# Hand off to the stock nginx entrypoint/command (nginx -g 'daemon off;').
exec "$@"
