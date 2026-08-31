// Configuração de conexão com o backend.
//
// - Mesma origem (nginx no Docker, Vercel com proxy): deixe vazio — todas as
//   chamadas partem para a própria origem (/api, /events).
// - Outra origem (dev local): o backend precisa de CORS_ALLOWED_ORIGINS com a
//   origem do frontend (ex.: http://localhost:3000).
//
// Em desenvolvimento, o `npm run dev` injeta o valor de TLM_API_BASE no
// placeholder abaixo automaticamente. Em produção o placeholder é trocado
// para vazio no build (nginx/Vercel).
(function () {
    const API_BASE = "__API_BASE__".replace(/\/+$/, "");
    window.TLM_API_BASE = API_BASE;

    if (!API_BASE) {
        // Mesma origem: nada a fazer.
        return;
    }

    // Prefixa chamadas de fetch relativas com a base da API. O EventSource
    // não envia headers, então o SSE já usa fetch + ReadableStream (auth.js).
    const nativeFetch = window.fetch.bind(window);
    window.fetch = function (input, init) {
        if (typeof input === "string" && input.startsWith("/")) {
            return nativeFetch(API_BASE + input, init);
        }
        if (input instanceof Request) {
            const url = new URL(input.url, window.location.origin);
            if (url.origin === window.location.origin) {
                return nativeFetch(new Request(API_BASE + url.pathname + url.search, input));
            }
        }
        return nativeFetch(input, init);
    };
})();