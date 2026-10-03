#!/usr/bin/env python3
"""One-time administrative export. Never called by the application."""
import argparse
import json
import os
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--source-env', required=True)
parser.add_argument('--output', required=True)
args = parser.parse_args()
config = {}
with open(args.source_env, encoding='utf-8') as source:
    for line in source:
        if '=' in line and not line.lstrip().startswith('#'):
            key, value = line.strip().split('=', 1)
            config[key] = value.strip().strip('"').strip("'")
url = config.get('SUPABASE_URL', '').rstrip('/')
key = config.get('SUPABASE_SERVICE_ROLE_KEY', '')
if not url.startswith('https://') or not key:
    raise SystemExit('Credenciais administrativas da origem ausentes.')

def read(path):
    request = urllib.request.Request(url + path, headers={'apikey': key, 'Authorization': 'Bearer ' + key})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)

users = []
page = 1
while True:
    batch = read(f'/auth/v1/admin/users?page={page}&per_page=100').get('users', [])
    users.extend(batch)
    if len(batch) < 100:
        break
    page += 1
profiles = {}
offset = 0
while True:
    batch = read(f'/rest/v1/profiles?select=*&order=id&offset={offset}&limit=100')
    profiles.update({p['id']: p for p in batch})
    if len(batch) < 100:
        break
    offset += 100
result = []
for user in users:
    profile = profiles.get(user['id'], {})
    metadata = user.get('app_metadata') or {}
    for field in ('role', 'active'):
        if field in profile and field in metadata and profile[field] != metadata[field]:
            raise SystemExit(f'Divergência de {field} para ID {user["id"]}; resolva antes de migrar.')
    result.append({
        'id': user['id'], 'email': user.get('email', '').strip().lower(),
        'displayName': profile.get('display_name') or '',
        'role': profile.get('role', metadata.get('role', 'subscriber')),
        'active': profile.get('active', metadata.get('active', False)),
        'notes': profile.get('notes') or '',
        'subscriptionExpiresAt': profile.get('subscription_expires_at') or metadata.get('subscription_expires_at'),
        'createdAt': user['created_at'], 'updatedAt': user.get('updated_at', user['created_at']),
    })
with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w', encoding='utf-8') as output:
    json.dump({'users': result}, output, ensure_ascii=False, indent=2)
print(f'{len(result)} identidades exportadas; nenhuma senha ou sessão foi exportada.')
