// Prazo fixo em Brasília: a oferta não reinicia ao abrir a página.
(() => {
    const endsAt = Date.parse('2026-09-23T00:00:00-03:00');
    function updateOffer() {
        const active = Date.now() < endsAt;
        document.querySelectorAll('[data-launch-offer]').forEach(el => { el.hidden = !active; });
        document.querySelectorAll('[data-after-launch]').forEach(el => { el.hidden = active; });
    }
    updateOffer();
    setInterval(updateOffer, 60000);
})();
