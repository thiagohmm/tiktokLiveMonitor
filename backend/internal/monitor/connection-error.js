function connectionFailure(err) {
    if (err?.reason === 'Rate Limited') {
        const retryAfterMs = Number.isFinite(err.retryAfter) && err.retryAfter > 0
            ? err.retryAfter : 60000;
        return {
            success: false,
            retryAfterMs,
            error: `Limite temporário de conexões. Nova tentativa em ${Math.ceil(retryAfterMs / 1000)}s.`
        };
    }
    return { success: false, error: `Falha ao conectar: ${err?.message || String(err)}` };
}

module.exports = { connectionFailure };
