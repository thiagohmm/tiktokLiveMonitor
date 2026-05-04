#!/usr/bin/env python3
import argparse
import asyncio
import inspect
import json
import os
import sys
import traceback

def write_result(payload, exit_code=0):
    print(json.dumps(payload, ensure_ascii=False))
    return exit_code

def parser():
    p = argparse.ArgumentParser(description="Silencia um usuário no chat TikTok LIVE usando TikTokLive.")
    p.add_argument("--username", required=True)
    p.add_argument("--target-user-id", required=True)
    p.add_argument("--session-id", required=True)
    p.add_argument("--duration", type=int, default=60)
    p.add_argument("--tt-target-idc", default=os.environ.get("TIKTOK_TT_TARGET_IDC", ""))
    p.add_argument("--room-id", default="")
    return p

async def maybe_await(value):
    if inspect.isawaitable(value):
        return await value
    return value

async def start_client(client, room_id):
    kwargs = {}
    if room_id:
        kwargs["room_id"] = room_id
    try:
        return await client.start(**kwargs)
    except TypeError:
        if room_id:
            return await client.start(room_id=room_id)
        return await client.start()

async def disconnect_client(client):
    for name in ("disconnect", "stop"):
        method = getattr(client, name, None)
        if not method:
            continue
        try:
            await maybe_await(method())
        except TypeError:
            await maybe_await(method(close_client=True))
        return

def compact(value, limit=1200):
    text = str(value)
    if len(text) <= limit:
        return text
    return text[:limit] + "...[truncated]"

def response_details(response):
    if not response:
        return None

    details = {}
    status_code = getattr(response, "status_code", None)
    if status_code is not None:
        details["status_code"] = status_code

    request = getattr(response, "request", None)
    request_url = getattr(request, "url", None)
    if request_url is not None:
        details["url"] = str(request_url)

    try:
        details["body"] = response.json()
    except Exception:
        text = getattr(response, "text", "")
        if text:
            details["body"] = compact(text)

    return details or None

def exception_payload(exc, context=""):
    message = str(exc) or exc.__class__.__name__
    if isinstance(exc, KeyError):
        message = f"Resposta inesperada sem chave {exc!s}"

    payload = {
        "type": exc.__class__.__name__,
        "message": message
    }
    if context:
        payload["context"] = context

    response = getattr(exc, "response", None)
    details = response_details(response)
    if details:
        payload["response"] = details

    cause = getattr(exc, "__cause__", None)
    if cause:
        payload["cause"] = {
            "type": cause.__class__.__name__,
            "message": str(cause) or cause.__class__.__name__
        }
        cause_response = response_details(getattr(cause, "response", None))
        if cause_response:
            payload["cause"]["response"] = cause_response

    return payload

async def mute_user(args):
    try:
        from TikTokLive import TikTokLiveClient
        from TikTokLive.client.web.web_settings import WebDefaults
        from TikTokLive.client.errors import SignAPIError
    except ImportError as exc:
        raise RuntimeError("TikTokLive não está instalado. Rode: pip install -r requirements-python.txt") from exc

    username = args.username.strip().lstrip("@")
    client = TikTokLiveClient(unique_id=f"@{username}")

    # Configura a sessão
    web = getattr(client, "web", client._web if hasattr(client, "_web") else None)
    if web and hasattr(web, "set_session"):
        web.set_session(args.session_id, args.tt_target_idc or None)

    # Lista de servidores de assinatura para tentar em caso de erro
    sign_servers = [
        "https://tiktok.eulerstream.com",
        "https://tiktok-sign.zerody.one",
        "https://tiktok-sign.workers.dev" # Fallback adicional
    ]

    room_id = args.room_id
    
    # Se não temos room_id, precisamos iniciar o cliente para descobri-lo
    task = None
    if not room_id:
        success = False
        last_error = None
        for server in sign_servers:
            WebDefaults.tiktok_sign_url = server
            
            # Atualiza o whitelist para o host atual, necessário para sessões autenticadas
            host = server.split("://")[1].split("/")[0]
            os.environ['WHITELIST_AUTHENTICATED_SESSION_ID_HOST'] = host

            try:
                task = await start_client(client, None)
                room_id = client.room_id
                success = True
                break
            except SignAPIError as e:
                last_error = e
                continue
            except Exception as e:
                last_error = e
                continue
        
        if not success:
            raise last_error or RuntimeError("Falha ao iniciar cliente TikTokLive (Erro de Sign API).")
    else:
        # Se já temos room_id, podemos tentar agir diretamente
        client._room_id = int(room_id)

    try:
        # Tentar silenciar usando o endpoint do Webcast
        # Como a v6 não expõe mute_user nativamente, fazemos o request manual assinado
        
        url = "https://webcast.tiktok.com/webcast/room/mute/"
        params = {
            "room_id": str(room_id),
            "target_user_id": str(args.target_user_id),
            "duration": str(args.duration),
            "mute_type": "1" # 1 = comment mute
        }

        response_data = None
        last_error = None
        
        # Tentar diferentes tipos de assinatura (xhr vs fetch) se necessário
        sign_types = ["xhr", "fetch"]

        for server in sign_servers:
            WebDefaults.tiktok_sign_url = server
            host = server.split("://")[1].split("/")[0]
            os.environ['WHITELIST_AUTHENTICATED_SESSION_ID_HOST'] = host

            for s_type in sign_types:
                try:
                    # O método request/post com sign_url=True usa o Sign Server para X-Bogus/msToken
                    response = await web.post(
                        url=url,
                        extra_params=params,
                        sign_url=True,
                        sign_url_method="POST",
                        sign_url_type=s_type
                    )
                    response_data = response.json()
                    if response_data:
                        break
                except Exception as e:
                    last_error = RuntimeError(json.dumps(
                        exception_payload(e, f"sign_server={server}, sign_type={s_type}"),
                        ensure_ascii=False
                    ))
                    continue
            if response_data:
                break
        
        if not response_data:
            raise last_error or RuntimeError("Falha ao enviar comando de silenciar.")

        status_code = response_data.get("status_code", -1)
        if status_code != 0:
            return {
                "ok": False, 
                "error": f"TikTok retornou erro (status_code={status_code})", 
                "response": response_data
            }

        return {"ok": True, "response": response_data}
    finally:
        try:
            await disconnect_client(client)
        finally:
            if task and hasattr(task, "cancel") and not task.done():
                task.cancel()

def main():
    args = parser().parse_args()
    if not args.session_id:
        return write_result({"ok": False, "error": "Cookie sessionid não encontrado."}, 2)
    try:
        result = asyncio.run(mute_user(args))
        return write_result(result, 0)
    except Exception as exc:
        print(traceback.format_exc(), file=sys.stderr)
        try:
            details = json.loads(str(exc))
        except Exception:
            details = exception_payload(exc)
        return write_result({
            "ok": False,
            "error": details.get("message") or str(exc) or exc.__class__.__name__,
            "details": details
        }, 1)

if __name__ == "__main__":
    sys.exit(main())
