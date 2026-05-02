#!/usr/bin/env python3
"""
Exemplos de uso (Parâmetros e Variáveis de Ambiente):

1. Mensagem direta (user_message):
   python scripts/chat-llm.py "Olá, como você está?"

2. Prompt de sistema customizado (-s | --system):
   python scripts/chat-llm.py -s "Você é um pirata" "Onde está o tesouro?"

3. Limite de tokens (--max-tokens):
   python scripts/chat-llm.py --max-tokens 10 "Conte uma piada"

4. Criatividade/Temperatura (-t | --temperature):
   python scripts/chat-llm.py -t 0.9 "Sugira um nome para um gato"

5. Saída em JSON (--json):
   python scripts/chat-llm.py --json "Analise este texto"

6. Ignorar espera pelo servidor (--no-wait):
   python scripts/chat-llm.py --no-wait "Teste rápido"

7. Servidor Remoto (Variáveis de Ambiente LLAMA_HOST e LLAMA_PORT):
   # Exemplo: Conectar a um servidor em outro IP e porta específica
   LLAMA_HOST=192.168.1.100 LLAMA_PORT=9000 python scripts/chat-llm.py "Olá servidor remoto"

Exemplos combinados:
   python scripts/chat-llm.py -s "Resuma" -t 0.1 --max-tokens 5 "O sol é uma estrela?"
   echo "Frase via pipe" | LLAMA_PORT=8081 python scripts/chat-llm.py
"""
import sys
import os
import json
import time
import urllib.request
import urllib.error
import argparse

# Configurações padrão
DEFAULT_SYSTEM_PROMPT = (
    "Você é moderador de uma live não cristã. Responda APENAS SIM ou NAO. "
    "SIM se a frase incomoda quem não é cristão (por exemplo proselitismo, condenação religiosa, "
    "menosprezo a outras crenças, ou empurrar Jesus/fé cristã de forma inadequada ao contexto).\n\n"
    "Regras de saída:\n"
    "- Responda APENAS com a palavra SIM ou NAO, sem aspas, explicações ou outro texto."
)

HOST = os.environ.get("LLAMA_HOST", "127.0.0.1")
PORT = os.environ.get("LLAMA_PORT", "8080")
BASE_URL = f"http://{HOST}:{PORT}"

def http_request(url, method="GET", data=None, headers=None):
    if headers is None:
        headers = {}
    
    req = urllib.request.Request(url, method=method, headers=headers)
    if data:
        if isinstance(data, (dict, list)):
            data = json.dumps(data).encode("utf-8")
            if "Content-Type" not in headers:
                headers["Content-Type"] = "application/json"
        elif isinstance(data, str):
            data = data.encode("utf-8")
        
        req.data = data
        for k, v in headers.items():
            req.add_header(k, v)

    try:
        with urllib.request.urlopen(req, timeout=300) as response:
            return response.getcode(), response.read().decode("utf-8")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8")
    except Exception as e:
        raise e

def wait_for_health_ready(timeout_ms=600000):
    start = time.time()
    url = f"{BASE_URL}/health"
    print(f"[chat-llm] Aguardando {HOST}:{PORT}/health …", file=sys.stderr)
    
    while (time.time() - start) * 1000 < timeout_ms:
        try:
            code, body = http_request(url)
            if code == 200:
                return
            
            elapsed = int(time.time() - start)
            if code == 503 and elapsed > 0 and elapsed % 15 == 0:
                print("[chat-llm] Modelo ainda carregando (503)…", file=sys.stderr)
        except Exception:
            pass
        time.sleep(1)
    
    raise TimeoutError(f"llama-server em {BASE_URL} não respondeu 200 em /health dentro do tempo.")

def assistant_text_from_chat_response(data):
    try:
        msg = data.get("choices", [{}])[0].get("message", {})
        content = msg.get("content", "") or ""
        reasoning = msg.get("reasoning_content", "") or ""
        
        out = content.strip() or reasoning.strip()
        return out if out else None
    except Exception:
        return None

def main():
    parser = argparse.ArgumentParser(description="Envia mensagens ao llama-server (OpenAI-compatible) em Python.")
    parser.add_argument("user_message", nargs="*", help="Mensagem do usuário")
    parser.add_argument("-s", "--system", help="Prompt de sistema")
    parser.add_argument("--max-tokens", type=int, help="Limite de tokens")
    parser.add_argument("-t", "--temperature", type=float, help="Temperatura")
    parser.add_argument("--json", action="store_true", help="Imprime JSON completo da API")
    parser.add_argument("--no-wait", action="store_true", help="Não espera /health == 200 antes do POST")
    
    # Suporte para "--" antes da mensagem (como no script node)
    # No argparse, se passarmos "--", ele para de processar opções.
    # Mas aqui o user_message já pega o que sobrou.
    
    args = parser.parse_args()

    system_explicit = args.system is not None
    system_prompt = args.system if system_explicit else DEFAULT_SYSTEM_PROMPT
    
    max_tokens = args.max_tokens
    if max_tokens is None:
        max_tokens = 256 if system_explicit else 32
        
    temperature = args.temperature
    if temperature is None:
        temperature = 0.7 if system_explicit else 0.1

    user_text = " ".join(args.user_message).strip()
    
    if not user_text and not sys.stdin.isatty():
        user_text = sys.stdin.read().strip()
    
    if not user_text:
        parser.print_help()
        sys.exit(1)

    if not args.no_wait:
        try:
            wait_for_health_ready()
        except TimeoutError as e:
            print(f"Erro: {e}", file=sys.stderr)
            sys.exit(1)

    user_content = user_text if system_explicit else f"Texto para analisar:\n{json.dumps(user_text)}"

    payload = {
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_content}
        ],
        "temperature": temperature,
        "max_tokens": max_tokens,
        "stream": False,
        "chat_template_kwargs": {"enable_thinking": False}
    }

    print(f"[chat-llm] POST /v1/chat/completions (inferência pode demorar no CPU)…", file=sys.stderr)
    
    try:
        code, body = http_request(f"{BASE_URL}/v1/chat/completions", method="POST", data=payload)
    except Exception as e:
        print(f"Erro na requisição: {e}", file=sys.stderr)
        sys.exit(1)

    if args.json:
        print(body)
        sys.exit(0 if 200 <= code < 300 else 1)

    if not (200 <= code < 300):
        print(f"Erro HTTP {code}: {body[:2000]}", file=sys.stderr)
        sys.exit(1)

    try:
        response_json = json.loads(body)
    except json.JSONDecodeError:
        print(f"Resposta não é JSON: {body[:500]}", file=sys.stderr)
        sys.exit(1)

    text = assistant_text_from_chat_response(response_json)
    if text is None:
        print("Resposta vazia. Tente aumentar --max-tokens.", file=sys.stderr)
        if not args.json:
            print(json.dumps(response_json, indent=2), file=sys.stderr)
        sys.exit(1)

    print(text.strip())

if __name__ == "__main__":
    main()
