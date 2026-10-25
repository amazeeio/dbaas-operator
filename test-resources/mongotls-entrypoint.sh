#!/bin/bash
set -e
cd /
mkdir -p /data/db
mongod --fork --logpath /tmp/mongod-init.log --bind_ip 127.0.0.1 --dbpath /data/db
mongo admin --quiet --eval "if (db.getUser('${MONGO_INITDB_ROOT_USERNAME}') == null) { db.createUser({user: '${MONGO_INITDB_ROOT_USERNAME}', pwd: '${MONGO_INITDB_ROOT_PASSWORD}', roles: [{role: 'root', db: 'admin'}]}) }"
mongod --shutdown --dbpath /data/db
exec mongod --auth --bind_ip_all --tlsMode requireTLS --tlsAllowConnectionsWithoutCertificates --tlsCertificateKeyFile /test-server1.pem --tlsCAFile /test-ca.pem
