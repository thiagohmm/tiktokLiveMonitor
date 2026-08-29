console.log('TikTok Live Monitor Renderer Loaded');

// Tradução dos nomes de presentes para PT-BR (mesma fonte do backend gifts.js)
const GIFT_TRANSLATIONS = {
    "rose": "Rosa",
    "heart": "Coração",
    "hand heart": "Coração de Mão",
    "finger heart": "Coração de Dedo",
    "love heart": "Coração de Amor",
    "beating heart": "Coração Batendo",
    "heartbeat": "Batimento",
    "sunglasses": "Óculos de Sol",
    "mirror": "Espelho",
    "cap": "Boné",
    "hat": "Chapéu",
    "money gun": "Metralhadora de Dinheiro",
    "galaxy": "Galáxia",
    "lion": "Leão",
    "helicopter": "Helicóptero",
    "race car": "Carro de Corrida",
    "sports car": "Carro Esportivo",
    "rocket": "Foguete",
    "confetti": "Confete",
    "fireworks": "Fogos de Artifício",
    "disc": "Disco",
    "disco": "Discoteca",
    "gift box": "Caixa de Presente",
    "present box": "Caixa de Presente",
    "handbag": "Bolsa",
    "crown": "Coroa",
    "castle": "Castelo",
    "whale": "Baleia",
    "dolphin": "Golfinho",
    "star": "Estrela",
    "microphone": "Microfone",
    "mic": "Microfone",
    "basketball": "Basquete",
    "football": "Futebol Americano",
    "soccer": "Futebol",
    "love bang": "Bomba de Amor",
    "love letter": "Carta de Amor",
    "i love you": "Eu Te Amo",
    "applause": "Palmas",
    "a shard of hope": "Um Fragmento de Esperança",
    "accelerator crown": "Coroa Aceleradora",
    "adam’s dream": "O Sonho de Adam",
    "alien buddy": "Amigo Alienígena",
    "amped up": "Animadinho",
    "amusement park": "Parque de Diversões",
    "animal band": "Banda Animal",
    "arabian cheetah": "Guepardo Árabe",
    "arcane card": "Carta Arcana",
    "arepas": "Arepas",
    "astrobear": "Urso Astro",
    "aurora groove": "Groove Aurora",
    "autumn leaves": "Folhas de Outono",
    "autumn picnic": "Piquenique de Outono",
    "baby chicks": "Pintinhos",
    "baby hippo": "Hipopotaminho",
    "backing monkey": "Macaco de Apoio",
    "baglama": "Baglama",
    "balloon crown": "Coroa de Balão",
    "banana peel": "Casca de Banana",
    "bat headwear": "Chapeuzinho de Morcego",
    "batting cutie": "Doce Batedor",
    "battle champion": "Campeão da Batalha",
    "batwing hat": "Chapéu de Asa de Morcego",
    "beach date": "Encontro na Praia",
    "beach day": "Dia de Praia",
    "beach maracas": "Maracas de Praia",
    "beat wings": "Asas de Batida",
    "become kitten": "Vira Gatinho",
    "beijo": "Beijo",
    "big shout out": "Saudação Especial",
    "black paw": "Pata Preta",
    "black wolf": "Lobo Preto",
    "blast drum": "Tambor Explosivo",
    "bloom brass": "Sopro Florescente",
    "bloom melody": "Melodia Florescente",
    "blooming heart": "Coração Florescendo",
    "blooming ribbons": "Laços Florescentes",
    "blossom fairy": "Fada das Flores",
    "blow a kiss": "Sopro de Beijo",
    "blow rosy kisses": "Sopro de Beijos da Rosie",
    "blue lightning": "Relâmpago Azul",
    "boné": "Boné",
    "bounce speakers": "Caixas de Som Saltitantes",
    "bouquet flower": "Buquê de Flores",
    "bowknot": "Laço",
    "box of destiny": "Caixa do Destino",
    "boxing gloves": "Luvas de Boxe",
    "bravo!": "Bravo!",
    "brazilian vibe cap": "Boné Vibe Brasileira",
    "brown paw": "Pata Marrom",
    "bubble gum": "Balinha",
    "bubbly kiss": "Beijo Espumante",
    "budding heart": "Coração Brota",
    "bunny crown": "Coroa de Coelhinho",
    "bunny dj": "DJ Coelhinho",
    "butterfly for you": "Borboleta Para Você",
    "butterfly vibe": "Vibe Borboleta",
    "by the glaziers": "Pelos Vidraceiros",
    "cactus shuffle": "Shuffle de Cacto",
    "café": "Café",
    "cake slice": "Fatia de Bolo",
    "candy blast": "Explosão de Doces",
    "candy bouquet": "Buquê de Doces",
    "candy loot": "Saque de Doces",
    "candy puffs": "Sonhos de Doces",
    "captured vocals": "Vocais Capturados",
    "capybara": "Capivara",
    "carro esportivo": "Carro Esportivo",
    "castle fantasy": "Fantasia de Castelo",
    "caterpillar chaos": "Caos da Lagarta",
    "catrina": "Catrina",
    "celebration hat": "Chapéu de Celebração",
    "celebration tape": "Fita de Celebração",
    "celebration time": "Hora da Celebração",
    "cello romance": "Romance de Cello",
    "charmer bow": "Laço Encantador",
    "chasing the dream": "Perseguindo o Sonho",
    "chatting popcorn": "Pipoca Tagarela",
    "cheeky pup": "Filhote Travesso",
    "cheer for you": "Torcida Para Você",
    "cheer mic": "Microfone de Torcida",
    "cheer you up": "Te Animando",
    "cheering crab": "Caranguejo Torcedor",
    "cheering towel": "Toalha de Torcida",
    "cheetah": "Guepardo",
    "chick stampede": "Esmagamento de Pintinhos",
    "chirpy kisses": "Beijos Chirotantes",
    "chocolate": "Chocolate",
    "chopin in the rain": "Chopin na Chuva",
    "chrono rewinder": "Rebobinador do Tempo",
    "city pop": "City Pop",
    "clap clap": "Clap Clap",
    "cloud dj": "DJ das Nuvens",
    "clover hat": "Chapéu de Trevo",
    "clown boogie": "Boogie de Palhaço",
    "club cheers": "Brindes do Clube",
    "club music": "Música do Clube",
    "club power": "Poder do Clube",
    "club victory": "Vitória do Clube",
    "coco": "Coco",
    "coconut drink": "Bebida de Coco",
    "coconut juice": "Suco de Coco",
    "coffee magic": "Magia do Café",
    "colorful wings": "Asas Coloridas",
    "come on!": "Vamos!",
    "community gift": "Presente da Comunidade",
    "community heart": "Coração da Comunidade",
    "community legends": "Lendas da Comunidade",
    "community power": "Poder da Comunidade",
    "community rally": "Aclamação da Comunidade",
    "community style": "Estilo da Comunidade",
    "confete": "Confete",
    "confetti bear": "Urso de Confete",
    "congratulations": "Parabéns",
    "cooper flies home": "Cooper Voa Para Casa",
    "coração": "Coração",
    "coração batendo": "Coração Batendo",
    "coração de dedo": "Coração de Dedo",
    "coração de mão": "Coração de Mão",
    "coral": "Coral",
    "corgi": "Corgi",
    "corgi's drone show": "Show de Drone do Corgi",
    "coroa": "Coroa",
    "courage potion": "Poção de Coragem",
    "cow-napping": "Roubo de Vaca",
    "cozy xmas set": "Kit Natal Aconchegante",
    "craft dreamer": "Sonhador Artesão",
    "creeper": "Creeper",
    "crocodile": "Crocodilo",
    "crowd cheering": "Torcida da Multidão",
    "crystal dreams": "Sonhos de Cristal",
    "crystal heart": "Coração de Cristal",
    "cub on clouds": "Filhote nas Nuvens",
    "cupid koala": "Koala Cupido",
    "cyber roar": "Rugido Cibernético",
    "daylight debut": "Estreia ao Sol",
    "desert blitzy": "Blitzy do Deserto",
    "desert cooper": "Cooper do Deserto",
    "desert diny": "Diny do Deserto",
    "desert survivals": "Sobreviventes do Deserto",
    "desert tom": "Tom do Deserto",
    "desert wolf": "Lobo do Deserto",
    "devoted heart": "Coração Devoto",
    "diamond flight": "Voo de Diamante",
    "diamond gun": "Pistola de Diamante",
    "diamond shield": "Escudo de Diamante",
    "diamond tree": "Árvore de Diamante",
    "divine fingers": "Dedos Divinos",
    "dj glasses": "Óculos de DJ",
    "dj set": "DJ Set",
    "dj wave": "Onda DJ",
    "djembe master": "Mestre Djembe",
    "doll new year greeting": "Saudação de Ano Novo da Boneca",
    "doughnut": "Donut",
    "dragon crown": "Coroa de Dragão",
    "dragon flame": "Chama do Dragão",
    "dream big": "Sonhe Grande",
    "dream ride": "Viagem dos Sonhos",
    "dreamy hat": "Chapéu Sonhador",
    "dreamy strings": "Cordas Sonhadoras",
    "drum genius": "Gênio do Tambor",
    "drum hamster": "Hamster Tamborilador",
    "drum pop": "Pop de Tambor",
    "duit raya": "Duit Raya",
    "dynamic music": "Música Dinâmica",
    "echo mom": "Mãe Eco",
    "eid gift box": "Caixa de Presente do EID",
    "electro vibes": "Vibes Eletrônicas",
    "electronic love song": "Canção de Amor Eletrônica",
    "ellie the elephant": "Ellie, a Elefanta",
    "encore clap": "Palmas do Bis",
    "encore ticket": "Ingresso de Bis",
    "energy capsule": "Cápsula de Energia",
    "epic pianist": "Pianista Épico",
    "exclusive spark": "Faísca Exclusiva",
    "face-pulling": "Puxando o Rosto",
    "fairy hide": "Disfarce de Fada",
    "fairy locket": "Medalhão de Fada",
    "fairy wings": "Asas de Fada",
    "falcon": "Falcão",
    "fan cat": "Gato Torcedor",
    "fandom cheer": "Torcida FANDOM",
    "fandom fan": "Fã FANDOM",
    "fandom fever": "Febre FANDOM",
    "fandom stamp": "Carimbo FANDOM",
    "fantastic fly love": "Voo do Amor Fantástico",
    "feather mask": "Máscara de Pena",
    "feather tiara": "Tiara de Pena",
    "fênix": "Fênix",
    "festival bracelet": "Pulseira do Festival",
    "fiery dragon": "Dragão Flamejante",
    "fiesta accordion": "Acordeão de Festa",
    "fire phoenix": "Fênix de Fogo",
    "firelight kingdom": "Reino da Luz do Fogo",
    "firepit blitzy": "Blitzy da Fogueira",
    "firepit cooper": "Cooper da Fogueira",
    "firepit diny": "Diny da Fogueira",
    "firepit tom": "Tom da Fogueira",
    "flamingo floaty": "Flamingo Flutuante",
    "flamingo groove": "Groove do Flamingo",
    "floating octopus": "Polvo Flutuante",
    "floral serenade": "Serenata Floral",
    "flower headband": "Tiara de Flores",
    "flower show": "Show de Flores",
    "fluffy buddies": "Amigos Fofochos",
    "fly love": "Voo do Amor",
    "flying jets": "Jatos Voadores",
    "fogos de artifício": "Fogos de Artifício",
    "fondue": "Fondue",
    "forest beginnings": "Começos da Floresta",
    "forest elf": "Elfo da Floresta",
    "forever rosa": "Rosa Para Sempre",
    "fox legend": "Lenda da Raposa",
    "freestyle": "Freestyle",
    "friendship necklace": "Colar de Amizade",
    "frog conductor": "Sapo Regente",
    "fruit friends": "Amigos Frutinhas",
    "fully bloomed sakura": "Sakura em Plena Florescimento",
    "funky hatter": "Chapéu Funky",
    "futebol americano": "Futebol Americano",
    "future city": "Cidade do Futuro",
    "future encounter": "Encontro do Futuro",
    "future journey": "Jornada do Futuro",
    "g.o.a.t. busker": "Músico de Rua G.O.A.T.",
    "galáxia": "Galáxia",
    "galaxy's bestest": "Amizade Galáctica",
    "garland headpiece": "Adorno de Flores",
    "gate to treasure": "Porta do Tesouro",
    "gem gun": "Pistola de Joias",
    "getting ready": "Preparando",
    "gingerbread man": "Homem de Biscoito",
    "girafa": "Girafa",
    "glorious chef": "Chef Glorioso",
    "glow stick": "Bastão Luminoso",
    "go hamster": "Vai, Hamster!",
    "go home": "Vá Para Casa",
    "go popular": "Vai Virar Popular",
    "goal highlight-us": "Golaço em Destaque (US)",
    "gold necklace": "Colar Dourado",
    "golden gallop": "Galope Dourado",
    "good job": "Bom Trabalho",
    "gorilla": "Gorila",
    "grand prix stage": "Palco Grand Prix",
    "grand show": "Show Grandioso",
    "greeting card": "Cartão de Saudação",
    "greeting heart": "Coração de Saudação",
    "groove clarinet": "Clarineta Groove",
    "groove guitar": "Guitarra Groove",
    "groove straw": "Canudo Groove",
    "guanajuato": "Guanajuato",
    "guarda-chuva": "Guarda-Chuva",
    "guardian's pledge": "Juramento do Guardião",
    "gyeongbokgung": "Gyeongbokgung",
    "halloween fun hat": "Chapéu Divertido de Halloween",
    "hands up": "Mãos Para Cima",
    "hanging lights": "Luzes Penduradas",
    "happy party": "Festa Alegre",
    "hat and mustache": "Chapéu e Bigode",
    "hat of joy": "Chapéu da Alegria",
    "health potion": "Poção de Vida",
    "heart gaze": "Olhar de Coração",
    "heart guitar": "Guitarra de Coração",
    "heart hood": "Capuz de Coração",
    "heart me": "Me Ama",
    "heart my earthling": "Amo Minha Terrestre",
    "heartbeat keys": "Teclas de Batida",
    "heartbeats": "Batimentos",
    "hearts": "Corações",
    "here we go": "Vamos Lá",
    "hero landing": "Pouso Heroico",
    "hi! rosie!": "Oi! Rosie!",
    "hip-hop hen": "Galinha Hip-Hop",
    "hive escape": "Fuga da Colmeia",
    "honey strummer": "Dedilhado Meloso",
    "hug it better": "Abraço Melhor",
    "hydrangea sea": "Mar de Hortênsias",
    "i like what i see": "Gosto do Que Vejo",
    "ice cream cone": "Sorvete de Cone",
    "ice cream mic": "Microfone de Sorvete",
    "ice cream truck": "Caminhão de Sorvete",
    "ignition check": "Checagem de Partida",
    "infinite heart": "Coração Infinito",
    "interstellar": "Interestelar",
    "interstellar trek": "Viagem Interestelar",
    "intimacy": "Intimidade",
    "invincible hammer": "Martelo Invencível",
    "it's match time": "É Hora do Jogo",
    "join butterflies": "Junte-se às Borboletas",
    "joker ball": "Bola do Coringa",
    "jollie the joy bean": "Jollie, o Feijão da Alegria",
    "jollie's community": "Comunidade da Jollie",
    "jollie's heartland": "Terra do Coração da Jollie",
    "journey pass": "Passe de Jornada",
    "joy floats": "Flutuações da Alegria",
    "juicy cap": "Boné Suculento",
    "julius the champion": "Julius, o Campeão",
    "jungle blitzy": "Blitzy da Selva",
    "jungle cooper": "Cooper da Selva",
    "jungle diny": "Diny da Selva",
    "jungle tom": "Tom da Selva",
    "kangaroo": "Canguru",
    "key master": "Mestre das Teclas",
    "kicker challenge": "Desafio do Batedor",
    "kindom of night": "Reino da Noite",
    "king leonardo": "Rei Leonardo",
    "kitten kneading": "Gatinho Sovando",
    "knight": "Cavaleiro",
    "kudos for my star": "Elogios Para Minha Estrela",
    "labor power": "Poder do Trabalho",
    "last riff roar": "Último Rugido do Riff",
    "laughing taco": "Taco Risonho",
    "league ball": "Bola da Liga",
    "league countdown": "Contagem Regressiva da Liga",
    "league fandom": "Fandom da Liga",
    "league trophy": "Troféu da Liga",
    "leão": "Leão",
    "legend marcellus": "Lenda Marcellus",
    "legendary aegis": "Égide Lendária",
    "leon and lili": "Leon e Lili",
    "leon and lion": "Leon e o Leão",
    "leon the kitten": "Leon, o Gatinho",
    "leon's sigil cape": "Capa do Símbolo de Leon",
    "leopard": "Leopardo",
    "let butterfly dances": "Deixe as Borboletas Dançarem",
    "level ship": "Navio de Nível",
    "level-up sparks": "Faíscas de Level-up",
    "level-up spectacle": "Espetáculo de Level-up",
    "level-up spotlight": "Holofote de Level-up",
    "light castle": "Castelo de Luz",
    "like-pop": "Like-Pop",
    "lili the leopard": "Lili, a Leopardo",
    "little crown": "Coroa Pequena",
    "live pro badge": "Emblema LIVE Pro",
    "live ranking crown": "Coroa do Ranking LIVE",
    "live ranking headband": "Faixa do Ranking LIVE",
    "live ranking medal": "Medalha do Ranking LIVE",
    "live ranking party": "Festa do Ranking LIVE",
    "live ranking ticket": "Ingresso do Ranking LIVE",
    "lobo": "Lobo",
    "look up": "Olhe Para Cima",
    "lost in your music": "Perdido na Sua Música",
    "love call": "Chamada de Amor",
    "love drop": "Gota de Amor",
    "love flight": "Voo do Amor",
    "love glasses": "Óculos do Amor",
    "love in scent": "Amor no Perfume",
    "love painting": "Pintura de Amor",
    "love you": "Te Amo",
    "love you so much": "Te Amo Muito",
    "lover’s lock": "Tranca do Amor",
    "lucky airdrop box": "Caixa de Airdrop da Sorte",
    "lucky crown": "Coroa da Sorte",
    "lucky pony": "Pônei da Sorte",
    "luvx cheer": "Torcida LUVX",
    "magic accordion": "Acordeão Mágico",
    "magic genie": "Gênio Mágico",
    "magic potion": "Poção Mágica",
    "magic prop": "Adereço Mágico",
    "magic rhythm": "Ritmo Mágico",
    "magic world": "Mundo Mágico",
    "magnifying glass": "Lupa",
    "majestic hearts": "Corações Majestosos",
    "manifesting": "Manifestando",
    "mark of love": "Selo do Amor",
    "marked with love": "Marcado com Amor",
    "maro spider man": "Maro Spider-Man",
    "marvelous confetti": "Confete Marveloso",
    "masquerade": "Máscara",
    "matador": "Matador",
    "match maniac": "Maníaco da Partida",
    "match master": "Mestre da Partida",
    "matchtacular!": "Matchtacular!",
    "melodic birds": "Pássaros Melódicos",
    "melody glasses": "Óculos de Melodia",
    "melody headset": "Fone de Melodia",
    "meowsic trumpet": "Trompete Meowsic",
    "meteor shower": "Chuva de Meteoros",
    "metralhadora de dinheiro": "Metralhadora de Dinheiro",
    "mic champ": "Campeão do Microfone",
    "mind blown": "Mente Explodida",
    "mishka bear": "Urso Mishka",
    "mom's bonnet": "Chapeuzinho da Mamãe",
    "motorcycle": "Motocicleta",
    "music  album": "Álbum Musical",
    "music album": "Álbum Musical",
    "music bubbles": "Bolhas de Música",
    "music burst": "Explosão Musical",
    "music cloud": "Nuvem Musical",
    "music conductor": "Regente Musical",
    "music mate": "Amigo Musical",
    "music on stage": "Música no Palco",
    "my dream stage": "Meu Palco dos Sonhos",
    "my first rose": "Minha Primeira Rosa",
    "mystery box": "Caixa Misteriosa",
    "mystery firework": "Foguete Misterioso",
    "name shoutout": "Saudação do Nome",
    "naughty chicken": "Galinha Travessa",
    "neon rockstar": "Rockstar Neon",
    "obelisk argentina": "Obelisco Argentina",
    "óculos de sol": "Óculos de Sol",
    "office penguin": "Pinguim do Escritório",
    "oldies": "Oldies (Clássicos)",
    "out pops jollie!": "Sai o Jollie!",
    "over the cloud": "Por Cima da Nuvem",
    "overreact": "Reação Exagerada",
    "palm breeze": "Brisa de Palma",
    "panda snap": "Panda Snap",
    "papa capybara": "Papai Capivara",
    "paper crane": "Grifo de Papel",
    "paris": "Paris",
    "party blossom": "Flor da Festa",
    "party bus": "Ônibus da Festa",
    "party laser": "Laser de Festa",
    "party on&on": "Festa Sempre",
    "party pony": "Pônei da Festa",
    "paw call": "Chamado da Pata",
    "pearl chime": "Sininho de Pérola",
    "pegasus": "Pégaso",
    "penguin snowpal": "Pinguim da Neve",
    "penlight": "Luz de Caneta",
    "perfume": "Perfume",
    "pinata": "Piñata",
    "pinch face": "Apertando o Rosto",
    "pink cowboy": "Cowboy Rosa",
    "pink dream": "Sonho Rosa",
    "pirate's treasure": "Tesouro do Pirata",
    "play for you": "Toco Para Você",
    "poetry score": "Partitura Poética",
    "pony lantern": "Luminária do Pônei",
    "pop parrot": "Papagaio Pop",
    "potato to fries": "Batata a Fritas",
    "power chip": "Chip de Poder",
    "powerful mind": "Mente Poderosa",
    "prairie blitzy": "Blitzy da Campina",
    "prairie cooper": "Cooper da Campina",
    "prairie diny": "Diny da Campina",
    "prairie tom": "Tom da Campina",
    "premium shuttle": "Balsa Premium",
    "pretzel": "Pão Torcido",
    "private jet": "Jato Privado",
    "proof of the king": "Prova do Rei",
    "punch cuddle": "Abraço Soco",
    "puppy kisses": "Beijos de Filhote",
    "racing debut": "Estreia nas Corridas",
    "racing helmet": "Capacete de Corrida",
    "rain doll": "Boneca da Chuva",
    "rainbow slide": "Escorregador Arco-Íris",
    "raving snail": "Lesma em Festa",
    "raya gift card": "Cartão-Prenda Raya",
    "red devil corgi": "Corgi Diabólico",
    "red lightning": "Relâmpago Vermelho",
    "reindeer milk": "Leite de Rena",
    "relaxed goose": "Ganso Relaxado",
    "retro headset": "Fone Retrô",
    "retro melody": "Melodia Retrô",
    "rhythm bot": "Robô de Ritmo",
    "rhythm crown": "Coroa do Ritmo",
    "rhythmic bear": "Urso Rítmico",
    "ribbit ribbit": "Croac Croac",
    "rin the snowborn": "Rin, a Nascida da Neve",
    "ring of honor-cube": "Cubo do Ring of Honor",
    "rock and roll": "Rock and Roll",
    "rock cats": "Gatos do Rock",
    "rock idol": "Ídolo do Rock",
    "rock star": "Rock Star",
    "rocking shroom": "Cogumelo Balançante",
    "rocky the rock bean": "Rocky, o Feijão do Rock",
    "rocky's punch": "Soco do Rocky",
    "roo mother": "Mãe Canguru",
    "rookies cup": "Taça dos Novatos",
    "rosa": "Rosa",
    "rosa nebula": "Nebulosa Rosa",
    "rose bear": "Urso de Rosa",
    "rose hand": "Mão de Rosa",
    "rose soundwave": "Onda Sonora de Rosa",
    "rosie on stage": "Rosie no Palco",
    "rosie the rose bean": "Rosie, o Feijão de Rosa",
    "rosie's concert": "Concerto da Rosie",
    "rust reforged": "Rust Reforged",
    "rust vs world": "Rust vs World",
    "safari park": "Parque Safari",
    "sage the smart bean": "Sage, o Feijão Sabido",
    "sage's coinbot": "Coinbot do Sage",
    "sage's slash": "Corte do Sage",
    "sage’s venture": "Empreitada do Sage",
    "sakura-style dj": "DJ Estilo Sakura",
    "sam in new city": "Sam na Nova Cidade",
    "samfaring tom": "Tom Samfaring",
    "santa owl surprise": "Surpresa da Coruja Natalina",
    "sax appeal": "Atrativo de Sax",
    "sax groove": "Groove de Sax",
    "scroll": "Pergaminho",
    "sea blitzy": "Blitzy do Mar",
    "sea cooper": "Cooper do Mar",
    "sea diny": "Diny do Mar",
    "sea tom": "Tom do Mar",
    "seahorse pop": "Cavalo-Marinho Pop",
    "semsemia": "Semsemia",
    "shell of a warrior": "Concha de Guerreiro",
    "shine bright": "Brilhe Muito",
    "shiny air balloon": "Balão Brilhante",
    "shoot the apple": "Acerte a Maçã",
    "side by side": "Lado a Lado",
    "signature jet": "Jato Assinado",
    "sing in sync": "Canto em Sincronia",
    "sing together": "Canto Junto",
    "singing frog": "Sapo Cantor",
    "singing magic": "Magia Cantada",
    "singing mushroom": "Cogumelo Cantor",
    "singing sax": "Sax Cantor",
    "sky drift": "Deriva no Céu",
    "skyforge citadel": "Cidadela do Skyforge",
    "sloth peek": "Preguiça Espiando",
    "slow motion": "Câmera Lenta",
    "smile latte": "Latte de Sorriso",
    "sneaky jockey": "Joqueiro Esperto",
    "snow bloom": "Flor da Neve",
    "snowmoon parasol": "Guarda-Sol da Lua de Neve",
    "so cute": "Tão Fofo",
    "soccer ball": "Bola de Futebol",
    "soccer holo": "Futebol Holo",
    "songs of live": "Músicas do LIVE",
    "sound spell": "Feitiço Sonoro",
    "space love": "Amor Espacial",
    "sparkle dance": "Dança Cintilante",
    "sparkle pony": "Pônei Cintilante",
    "spartan helmet": "Elmo Espártaco",
    "spider web": "Teia de Aranha",
    "spider web 2.0": "Teia de Aranha 2.0",
    "spidey pin": "Pin do Spidey",
    "spin seal": "Selo Giratório",
    "spinning soccer": "Futebol Giratório",
    "spooky cat": "Gato Assustador",
    "spring bouquet": "Buquê da Primavera",
    "spring sprout": "Brotinho da Primavera",
    "squirrel": "Esquilo",
    "stadium": "Estádio",
    "stage of ring": "Palco do Ring",
    "stage of spiderman": "Palco do Spider-Man",
    "stage wiggle": "Balanço de Palco",
    "star goggles": "Óculos de Estrela",
    "star of red carpet": "Estrela do Tapete Vermelho",
    "star throne": "Trono da Estrela",
    "starry fluff": "Pelinho Estrelado",
    "starry seal": "Selo Estrelado",
    "storm blade": "Lâmina da Tempestade",
    "stormwave armor": "Armadura da Onda Tempestuosa",
    "strike a pose": "Faça uma Pose",
    "strong finish": "Finalização Forte",
    "style me up": "Me Estiliza",
    "sugar whiskers": "Bigodinhos de Açúcar",
    "suitcase": "Mala",
    "summoning horn": "Chifre de Invocação",
    "sundae bowl": "Tigela de Sundae",
    "sunny side up": "Ovo Do Lado do Sol",
    "sunset speedway": "Pista do Pôr do Sol",
    "super dad": "Super Pai",
    "super popular": "Super Popular",
    "superwoman": "Supermulher",
    "surfing penguin": "Pinguim Surfeiro",
    "surprise baby mob": "Turma de Bebezinhos Surpresa",
    "swan": "Cisne",
    "sweet flutter": "Voo Doce",
    "swing cello": "Cello Swing",
    "take the mic": "Pega o Microfone",
    "talking heartbeat": "Batimento Falante",
    "team bracelet": "Pulseira do Time",
    "tempo flute": "Flauta de Tempo",
    "the trial of sea": "O Julgamento do Mar",
    "thumbs up": "Da Paz",
    "thunder falcon": "Falcão do Trovão",
    "tidecaller trident": "Tridente do Chamador das Marés",
    "tiger lift": "Levantada do Tigre",
    "tiktok red carpet": "Tapete Vermelho do TikTok",
    "tiktok shuttle": "Balsa do TikTok",
    "tiktok stars": "Estrelas do TikTok",
    "tiktok universe": "Universo TikTok",
    "tiktok universe+": "Universo TikTok+",
    "time warp": "Distorção do Tempo",
    "tiny diny": "Diny Minúscula",
    "tom bear beret": "Boina do Tom Urso",
    "tom thunderfoot": "Tom Pés de Trovão",
    "tom's hug": "Abraço do Tom",
    "traffic cone": "Cone de Trânsito",
    "train": "Trem",
    "travel with you": "Viagem Com Você",
    "treasure clover": "Trevo do Tesouro",
    "treasure's key": "Chave do Tesouro",
    "treasured voice": "Voz Preciosa",
    "trending figure": "Figura em Alta",
    "tropical mask": "Máscara Tropical",
    "tundra blitzy": "Blitzy da Tundra",
    "tundra cooper": "Cooper da Tundra",
    "tundra diny": "Diny da Tundra",
    "tundra tom": "Tom da Tundra",
    "ukulele player": "Músico de Ukulelê",
    "ultimate fandom": "FANDOM Definitivo",
    "umbrella of love": "Guarda-Chuva do Amor",
    "under control": "Sob Controle",
    "undersea kingdom": "Reino Submarino",
    "unicorn fantasy": "Fantasia de Unicórnio",
    "united heart": "Coração Unido",
    "valerian's oath": "Juramento de Valerian",
    "valiant odyssey": "Odisseia Valente",
    "vespa tater": "Batata Vespa",
    "vibrant stage": "Palco Vibrante",
    "viking hammer": "Martelo Viking",
    "vintage flight": "Voo Vintage",
    "vinyl flip": "Revirando o Vinil",
    "vocal bear": "Urso Vocal",
    "vr goggles": "Óculos de RV",
    "w": "W",
    "wakey mallow": "Marshmallow Acordador",
    "warm cocoa": "Cacau Quentinho",
    "water buffalo": "Búfalo",
    "watermelon love": "Amor de Melancia",
    "wave firework": "Foguete em Onda",
    "wave lights": "Luzes em Onda",
    "welcome dallah": "Boas-Vindas Dallah",
    "whale diving": "Baleia Mergulhando",
    "white rose": "Rosa Branca",
    "wild mic": "Microfone Selvagem",
    "wind on kemenche": "Vento na Kemençe",
    "wink charm": "Encanto de Piscadela",
    "wink wink": "Pisca Pisca",
    "wishing cake": "Bolo dos Desejos",
    "work hard play harder": "Esforce Mais, Brinque Mais",
    "xmas tree hat": "Chapéu de Pinheirinho",
    "xxxl flowers": "Flores XXXL",
    "yellow lightning": "Relâmpago Amarelo",
    "you’re amazing": "Você É Incrível",
    "you're awesome": "Você É Massa",
    "you're so fly": "Você É Fantástico",
    "your concert": "Seu Concerto",
    "yuki's vigilance": "Vigilância da Yuki",
    "zeus": "Zeus"
};

function translateGiftName(name) {
    if (typeof name !== 'string') return name;
    const trimmed = name.trim();
    if (!trimmed) return name;
    return GIFT_TRANSLATIONS[trimmed.toLowerCase()] || trimmed;
}

function ensureBrowserChart() {
    if (typeof window.Chart !== 'undefined') {
        return Promise.resolve();
    }
    return new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = `${window.location.origin}/vendor/chart.js`;
        script.onload = () => resolve();
        script.onerror = () => reject(new Error('Não foi possível carregar Chart.js.'));
        document.head.appendChild(script);
    });
}

const usernameInput = document.getElementById('username');
const connectBtn = document.getElementById('connectBtn');
const disconnectBtn = document.getElementById('disconnectBtn');
const listenBtn = document.getElementById('listenBtn');
const statusDiv = document.getElementById('status');
const userTableBody = document.getElementById('userTableBody');
const allGiftsTableBody = document.getElementById('allGiftsTableBody');
const pinnedCommentsTableBody = document.getElementById('pinnedCommentsTableBody');
const correlationMessagesTableBody = document.getElementById('correlationMessagesTableBody');
const rankingTableBody = document.getElementById('rankingTableBody');
const targetExpirationMinutesInput = document.getElementById('targetExpirationMinutes');
const chartCanvas = document.getElementById('messageChart');
const targetGiftHistoryBtn = document.getElementById('targetGiftHistoryBtn');
const targetGiftsList = document.getElementById('targetGiftsList');
const availableGiftSelect = document.getElementById('availableGiftSelect');
const addTargetGiftBtn = document.getElementById('addTargetGiftBtn');
const pinnedCommentHistoryBtn = document.getElementById('pinnedCommentHistoryBtn');
const goalTitleInput = document.getElementById('goalTitleInput');
const goalGiftSelect = document.getElementById('goalGiftSelect');
const goalTargetInput = document.getElementById('goalTargetInput');
const goalSaveBtn = document.getElementById('goalSaveBtn');
const goalResetBtn = document.getElementById('goalResetBtn');
const goalActivesList = document.getElementById('goalActivesList');
const goalHistoryWrap = document.getElementById('goalHistoryWrap');
const goalHistoryList = document.getElementById('goalHistoryList');
const goalMilestoneRows = Array.from(document.querySelectorAll('.goal-milestone-row'));
const historyModalBackdrop = document.getElementById('historyModalBackdrop');
const historyModalTitle = document.getElementById('historyModalTitle');
const historyModalBody = document.getElementById('historyModalBody');
const historyModalCloseBtn = document.getElementById('historyModalCloseBtn');
const profileModalBackdrop = document.getElementById('profileModalBackdrop');
const profileModalBody = document.getElementById('profileModalBody');
const profileModalCloseBtn = document.getElementById('profileModalCloseBtn');
const deleteLiveModalBackdrop = document.getElementById('deleteLiveModalBackdrop');
const deleteLiveModalMessage = document.getElementById('deleteLiveModalMessage');
const deleteLiveModalCancelBtn = document.getElementById('deleteLiveModalCancelBtn');
const deleteLiveModalConfirmBtn = document.getElementById('deleteLiveModalConfirmBtn');
const deleteLiveModalCloseBtn = document.getElementById('deleteLiveModalCloseBtn');
let deleteLivePending = null;
const giftSearchInput = document.getElementById('giftSearchInput');
const allGiftsSection = document.getElementById('allGiftsSection');
const allGiftsTableContainer = document.getElementById('allGiftsTableContainer');

// --- New feature elements ---
const adminLivesTableBody = document.getElementById('adminLivesTableBody');
const adminLivesRefreshBtn = document.getElementById('adminLivesRefreshBtn');
const adminLivesMoreBtn = document.getElementById('adminLivesMoreBtn');
let adminLivesLimit = 100;
const refreshRankingBtn = document.getElementById('refreshRankingBtn');
const generateReportBtn = document.getElementById('generateReportBtn');
const reportWrap = document.getElementById('reportWrap');
const reportSummary = document.getElementById('reportSummary');
const reportText = document.getElementById('reportText');
const reportError = document.getElementById('reportError');

let chart;
let messageCount = 0;
let chartData = Array(60).fill(0);
let giftCount = 0;
let giftChartData = Array(60).fill(0);
let autoRemoveTimers = {};
let pinnedCommentTimers = {};
let flaggedMessageTimers = {};
let targetGiftHistory = [];
let pinnedCommentHistory = [];
let listenedMessages = [];
let listenedUserId = '';
let listenDraftValue = '';
let liveUsers = new Map();
let activeModalType = null;
let isAddingTargetGift = false;

const LIVE_USERS_MAX = 200;
let renderListenModalTimeout = null;

function throttledRenderListenModal() {
    if (renderListenModalTimeout) {
        clearTimeout(renderListenModalTimeout);
    }
    renderListenModalTimeout = setTimeout(() => {
        renderListenModal({ preserveFocus: true });
        renderListenModalTimeout = null;
    }, 150);
}

function normalizeListenUser(value) {
    return String(value || '').trim().replace(/^@+/, '').toLowerCase();
}

function normalizeFollowerFlag(value) {
    if (value === true || value === 1 || value === '1' || value === 'true') {
        return true;
    }
    if (value === false || value === 0 || value === '0' || value === 'false') {
        return false;
    }
    return null;
}

function mergeFollowerStatus(previous, next) {
    const incoming = normalizeFollowerFlag(next);
    const current = normalizeFollowerFlag(previous);
    if (incoming === true) {
        return true;
    }
    if (incoming === false && current !== true) {
        return false;
    }
    if (current != null) {
        return current;
    }
    return incoming;
}

function followerStatusForDisplay(data) {
    const key = normalizeListenUser((data && (data.uniqueId || data.nickname)) || '');
    const stored = key ? liveUsers.get(key) : null;
    if (stored && stored.isFollower != null) {
        return stored.isFollower;
    }
    return normalizeFollowerFlag(data && data.isFollower);
}

function ensureFollowerBadge(userTd, data) {
    if (!userTd) {
        return;
    }
    const badge = createFollowerBadge(followerStatusForDisplay(data));
    if (!badge) {
        return;
    }
    const existing = userTd.querySelector('.badge-follower, .badge-not-follower');
    if (existing) {
        existing.replaceWith(badge);
        return;
    }
    userTd.appendChild(badge);
}

function rememberLiveUser(data) {
    if (!data) {
        return;
    }

    const uniqueId = String(data.uniqueId || '').trim().replace(/^@+/, '');
    const nickname = String(data.nickname || uniqueId || '').trim();
    const key = normalizeListenUser(uniqueId || nickname);

    if (!key) {
        return;
    }

    const previous = liveUsers.get(key) || {};
    liveUsers.set(key, {
        uniqueId: uniqueId || previous.uniqueId || '',
        nickname: nickname || previous.nickname || uniqueId || 'Nao identificado',
        isFollower: mergeFollowerStatus(previous.isFollower, data.isFollower),
        lastSeen: Date.now()
    });

    // Limitar tamanho do Map para evitar uso excessivo de memória
    if (liveUsers.size > LIVE_USERS_MAX) {
        const entries = Array.from(liveUsers.entries());
        entries.sort((a, b) => (a[1].lastSeen || 0) - (b[1].lastSeen || 0));
        const toRemove = entries.slice(0, liveUsers.size - LIVE_USERS_MAX);
        toRemove.forEach(([key]) => liveUsers.delete(key));
    }

    if (activeModalType === 'listen') {
        throttledRenderListenModal();
    }
}

function getLiveUserMatches(query) {
    const normalizedQuery = normalizeListenUser(query);
    return Array.from(liveUsers.values())
        .filter(user => {
            if (!normalizedQuery) {
                return true;
            }

            return normalizeListenUser(user.uniqueId).includes(normalizedQuery) ||
                normalizeListenUser(user.nickname).includes(normalizedQuery);
        })
        .sort((a, b) => (b.lastSeen || 0) - (a.lastSeen || 0))
        .slice(0, 50);
}

function trimHistory(items) {
    if (items.length > 15) {
        items.length = 15;
    }
}

function appendEmptyState(parent) {
    const p = document.createElement('p');
    p.className = 'modal-empty';
    p.textContent = 'Nenhum registro ainda.';
    parent.appendChild(p);
}

function createModalList(items, renderItem) {
    const list = document.createElement('div');
    list.className = 'modal-list';

    if (!items.length) {
        appendEmptyState(list);
        return list;
    }

    items.forEach(item => {
        const row = document.createElement('div');
        row.className = 'modal-item';
        renderItem(row, item);
        list.appendChild(row);
    });

    return list;
}

function renderUserLine(row, nickname, uniqueId, isFollower) {
    const strong = document.createElement('strong');
    strong.className = 'user-name';
    const userText = nickname || uniqueId || 'Nao identificado';
    strong.textContent = uniqueId ? `${userText} (@${uniqueId})` : userText;
    if (uniqueId) {
        strong.classList.add('user-link');
        strong.title = 'Ver perfil';
        strong.addEventListener('click', () => openProfile(uniqueId));
    }
    row.appendChild(strong);

    const badge = createFollowerBadge(isFollower);
    if (badge) {
        row.appendChild(badge);
    }
}

function createFollowerBadge(isFollower) {
    const flag = normalizeFollowerFlag(isFollower);
    if (flag === true) {
        const span = document.createElement('span');
        span.className = 'badge badge-follower';
        span.textContent = 'Segue';
        return span;
    }
    if (flag === false) {
        const span = document.createElement('span');
        span.className = 'badge badge-not-follower';
        span.textContent = 'Não Segue';
        return span;
    }
    return null;
}

function formatSaoPauloDateTime(value) {
    if (value == null || value === '') {
        return '—';
    }
    const date = value instanceof Date ? value : new Date(value);
    if (Number.isNaN(date.getTime())) {
        return '—';
    }
    return new Intl.DateTimeFormat('pt-BR', {
        timeZone: 'America/Sao_Paulo',
        day: '2-digit',
        month: '2-digit',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
    }).format(date);
}

function targetGiftResponseLabel(responseType) {
    if (responseType === 'manual') {
        return 'Respondido manualmente';
    }
    if (responseType === 'automatic') {
        return 'Respondido automaticamente';
    }
    return 'Pendente';
}

function targetGiftResponseClass(responseType) {
    if (responseType === 'manual' || responseType === 'automatic') {
        return responseType;
    }
    return 'pending';
}

async function markTargetGiftAnswered(historyId, responseType) {
    const id = Number(historyId);
    if (!Number.isFinite(id) || id <= 0) {
        return;
    }
    try {
        await fetch('/api/target-gift-history/answer', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id, responseType })
        });
    } catch (error) {
        console.error('[Frontend] Falha ao registrar resposta do presente alvo:', error);
    }
}

async function loadTargetGiftHistoryFromApi() {
    try {
        const response = await fetch('/api/target-gift-history?limit=50');
        if (!response.ok) {
            throw new Error(`status ${response.status}`);
        }
        const items = await response.json();
        return Array.isArray(items) ? items : [];
    } catch (error) {
        console.error('[Frontend] Falha ao carregar histórico de presentes alvos:', error);
        return [];
    }
}

async function renderGiftHistory() {
    historyModalTitle.textContent = 'Histórico de Presentes Alvos';
    historyModalBody.replaceChildren();

    const loading = document.createElement('p');
    loading.className = 'modal-empty';
    loading.textContent = 'Carregando histórico...';
    historyModalBody.appendChild(loading);

    const items = await loadTargetGiftHistoryFromApi();
    if (activeModalType !== 'target-gifts') {
        return;
    }

    historyModalBody.replaceChildren(createModalList(items, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId, item.isFollower);

        const gift = document.createElement('span');
        gift.textContent = item.giftName || 'Presente Alvo';
        row.appendChild(gift);

        const meta = document.createElement('div');
        meta.className = 'modal-item-meta';

        const received = document.createElement('span');
        received.textContent = `Recebido: ${formatSaoPauloDateTime(item.receivedAt)} (SP)`;
        meta.appendChild(received);

        const answered = document.createElement('span');
        answered.textContent = `Respondido: ${formatSaoPauloDateTime(item.answeredAt)} (SP)`;
        meta.appendChild(answered);
        row.appendChild(meta);

        const status = document.createElement('span');
        status.className = `modal-item-status ${targetGiftResponseClass(item.responseType)}`;
        status.textContent = targetGiftResponseLabel(item.responseType);
        row.appendChild(status);
    }));
}

async function renderPinnedCommentHistory() {
    historyModalTitle.textContent = 'Histórico de Comentários Fixados';
    historyModalBody.replaceChildren();

    const loading = document.createElement('p');
    loading.className = 'modal-empty';
    loading.textContent = 'Carregando histórico...';
    historyModalBody.appendChild(loading);

    const items = await loadPinnedCommentsFromApi();
    if (activeModalType !== 'pinned-comments') {
        return;
    }

    historyModalBody.replaceChildren(createModalList(items, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId, item.isFollower);

        const comment = document.createElement('span');
        comment.textContent = item.comment || '[sem texto identificado]';
        row.appendChild(comment);

        const meta = document.createElement('div');
        meta.className = 'modal-item-meta';
        const when = document.createElement('span');
        when.textContent = `${formatSaoPauloDateTime(item.timestamp)} (SP)`;
        meta.appendChild(when);
        row.appendChild(meta);
    }));
}

function setListenedUser(value) {
    const nextUserId = normalizeListenUser(value);
    if (nextUserId !== listenedUserId) {
        listenedMessages = [];
    }
    listenedUserId = nextUserId;
}

function renderLiveUserSelector(input) {
    const wrapper = document.createElement('div');
    wrapper.className = 'listen-user-panel';

    const users = getLiveUserMatches(input.value);
    if (!liveUsers.size) {
        const empty = document.createElement('p');
        empty.className = 'modal-empty';
        empty.textContent = 'Nenhum usuário visto na live ainda.';
        wrapper.appendChild(empty);
        return wrapper;
    }

    if (!users.length) {
        const empty = document.createElement('p');
        empty.className = 'modal-empty';
        empty.textContent = 'Nenhum usuário encontrado.';
        wrapper.appendChild(empty);
        return wrapper;
    }

    users.forEach(user => {
        const button = document.createElement('button');
        button.className = 'listen-user-option';
        button.type = 'button';

        const name = document.createElement('strong');
        name.textContent = user.nickname || user.uniqueId || 'Nao identificado';
        button.appendChild(name);

        const badge = createFollowerBadge(user.isFollower);
        if (badge) {
            button.appendChild(badge);
        }

        if (user.uniqueId) {
            const handle = document.createElement('span');
            handle.textContent = `@${user.uniqueId}`;
            button.appendChild(handle);
        }

        button.addEventListener('click', () => {
            listenDraftValue = user.uniqueId ? `@${user.uniqueId}` : user.nickname;
            setListenedUser(listenDraftValue);
            renderListenModal({ preserveFocus: true });
        });

        wrapper.appendChild(button);
    });

    return wrapper;
}

function renderListenModal(options = {}) {
    historyModalTitle.textContent = 'Escuta';
    historyModalBody.replaceChildren();

    const form = document.createElement('form');
    form.className = 'listen-form';

    const input = document.createElement('input');
    input.type = 'text';
    input.placeholder = '@usuario';
    input.autocomplete = 'off';
    input.value = listenDraftValue;
    input.addEventListener('input', () => {
        listenDraftValue = input.value;
        throttledRenderListenModal();
    });

    const button = document.createElement('button');
    button.type = 'submit';
    button.textContent = 'Escutar';

    form.appendChild(input);
    form.appendChild(button);
    form.addEventListener('submit', event => {
        event.preventDefault();
        setListenedUser(input.value);
        listenDraftValue = listenedUserId ? `@${listenedUserId}` : '';
        renderListenModal();
    });

    historyModalBody.appendChild(form);
    historyModalBody.appendChild(renderLiveUserSelector(input));
    historyModalBody.appendChild(createModalList(listenedMessages, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId, item.isFollower);
        const comment = document.createElement('span');
        comment.textContent = item.comment || '';
        row.appendChild(comment);
    }));

    if (options.preserveFocus) {
        input.focus();
        input.setSelectionRange(input.value.length, input.value.length);
    }
}

function renderActiveModal() {
    if (activeModalType === 'target-gifts') {
        renderGiftHistory();
    } else if (activeModalType === 'pinned-comments') {
        renderPinnedCommentHistory();
    } else if (activeModalType === 'listen') {
        renderListenModal();
    }
}

function openHistoryModal(type) {
    activeModalType = type;
    if (type === 'listen') {
        listenDraftValue = listenedUserId ? `@${listenedUserId}` : '';
    }
    renderActiveModal();
    historyModalBackdrop.classList.add('is-open');
    historyModalBackdrop.setAttribute('aria-hidden', 'false');
}

function closeProfileModal() {
    if (!profileModalBackdrop) return;
    profileModalBackdrop.classList.remove('is-open');
    profileModalBackdrop.setAttribute('aria-hidden', 'true');
    profileModalBody.innerHTML = '';
}

function closeHistoryModal() {
    historyModalBackdrop.classList.remove('is-open');
    historyModalBackdrop.setAttribute('aria-hidden', 'true');
    activeModalType = null;
}

async function openProfile(uniqueId) {
    if (!profileModalBackdrop || !profileModalBody) return;
    profileModalBody.innerHTML = '<p style="color:var(--text-muted)">Carregando perfil...</p>';
    profileModalBackdrop.classList.add('is-open');
    profileModalBackdrop.setAttribute('aria-hidden', 'false');
    try {
        const response = await fetch('/api/profile?uid=' + encodeURIComponent(String(uniqueId)));
        const data = await response.json();
        renderProfile(data);
    } catch (error) {
        profileModalBody.innerHTML = '<p style="color:var(--pink)">Falha ao carregar o perfil do usuário.</p>';
        console.error('[Frontend] Falha ao carregar perfil:', error);
    }
}

function renderProfile(profile) {
    if (!profileModalBody) return;
    profileModalBody.innerHTML = '';

    const header = document.createElement('div');
    header.style.marginBottom = '14px';
    header.innerHTML =
        '<div style="font-size:1.15em;font-weight:700;">' + escapeHtml(profile.nickname || profile.uniqueId || 'Participante') +
        ' <span style="color:var(--text-muted);font-weight:400;font-size:0.8em;">@' + escapeHtml(profile.uniqueId || '') + '</span></div>';
    const riskBadge = document.createElement('span');
    riskBadge.className = 'risk-badge ' + riskBadgeClass(profile.riskLevel);
    riskBadge.textContent = riskLabel(profile.riskLevel);
    header.appendChild(riskBadge);
    profileModalBody.appendChild(header);

    const stats = document.createElement('div');
    stats.className = 'report-summary';
    stats.style.marginBottom = '16px';
    const giftValue = profile.totalGiftUnits != null
        ? profile.totalGiftUnits
        : (profile.totalGifts != null ? profile.totalGifts : 0);
    const statItems = [
        { value: profile.totalMessages != null ? profile.totalMessages : 0, label: 'Mensagens' },
        { value: giftValue, label: 'Presentes' },
        { value: profile.totalLikes != null ? profile.totalLikes : 0, label: 'Curtidas' },
        { value: profile.totalShares != null ? profile.totalShares : 0, label: 'Compartilhamentos' },
        { value: (profile.lastLives || []).length, label: 'Vidas participadas' }
    ];
    statItems.forEach(stat => {
        const box = document.createElement('div');
        box.className = 'report-stat';
        box.innerHTML = '<div class="stat-value">' + escapeHtml(String(stat.value)) + '</div><div class="stat-label">' + escapeHtml(stat.label) + '</div>';
        stats.appendChild(box);
    });
    profileModalBody.appendChild(stats);

    // Últimas vidas
    const lives = profile.lastLives || [];
    if (lives.length) {
        const h = document.createElement('h4');
        h.textContent = 'Últimas vidas';
        h.style.margin = '12px 0 6px';
        h.style.fontSize = '0.9em';
        h.style.color = 'var(--text-muted)';
        profileModalBody.appendChild(h);
        lives.forEach(live => {
            const row = document.createElement('div');
            row.className = 'suggestion-card';
            row.style.borderLeftColor = 'var(--pink)';
            row.innerHTML = '<div style="font-weight:600;">' + escapeHtml(live.liveName || 'Live') + '</div>' +
                '<div style="font-size:0.8em;color:var(--text-muted);">' +
                (live.messages != null ? live.messages + ' mensagens, ' : '') +
                (live.gifts != null ? live.gifts + ' presentes. ' : '') +
                ('Primeira: ' + (live.firstSeen || '—') + ' • Última: ' + (live.lastSeen || '—')) +
                '</div>';
            profileModalBody.appendChild(row);
        });
    }

    // Alertas / infrações
    const alerts = profile.alerts || [];
    if (alerts.length) {
        const h2 = document.createElement('h4');
        h2.textContent = 'Alertas de moderação (' + alerts.length + ')';
        h2.style.margin = '12px 0 6px';
        h2.style.fontSize = '0.9em';
        h2.style.color = 'var(--text-muted)';
        profileModalBody.appendChild(h2);
        alerts.slice(0, 15).forEach(alert => {
            const row = document.createElement('div');
            row.className = 'suggestion-card';
            row.style.borderLeftColor = 'var(--pink)';
            row.innerHTML = '<div style="font-size:0.85em;">' + escapeHtml(alert.category || 'Infração') + '</div>' +
                '<div style="font-size:0.8em;color:var(--text-muted);">' + escapeHtml(alert.comment || '') + '</div>';
            profileModalBody.appendChild(row);
        });
    }

    // Últimas 10 mensagens
    const messages = profile.messages || [];
    if (messages.length) {
        const h3 = document.createElement('h4');
        h3.textContent = 'Últimas ' + Math.min(10, messages.length) + ' mensagens';
        h3.style.margin = '12px 0 6px';
        h3.style.fontSize = '0.9em';
        h3.style.color = 'var(--text-muted)';
        profileModalBody.appendChild(h3);
        messages.slice(0, 10).forEach(msg => {
            const row = document.createElement('div');
            row.className = 'suggestion-card';
            row.style.borderLeftColor = 'var(--cyan)';
            row.textContent = (msg.username || msg.uniqueId || '') + (msg.timestamp ? ' — ' + msg.timestamp : '') + ': ' + (msg.message || '');
            profileModalBody.appendChild(row);
        });
    }
}

function addTargetGiftToHistory(user) {
    // Persistido no backend; o modal carrega de /api/target-gift-history.
    if (activeModalType === 'target-gifts') {
        renderGiftHistory();
    }
}

function addPinnedCommentToHistory() {
    if (activeModalType === 'pinned-comments') {
        renderPinnedCommentHistory();
    }
}

function handleListenedMessage(data) {
    if (!listenedUserId || !data) {
        return;
    }

    if (normalizeListenUser(data.uniqueId) !== listenedUserId) {
        return;
    }

    listenedMessages.unshift({
        uniqueId: data.uniqueId || '',
        nickname: data.nickname || data.uniqueId || 'Nao identificado',
        comment: data.comment || '',
        timestamp: data.timestamp || Date.now()
    });
    trimHistory(listenedMessages);
    if (activeModalType === 'listen') {
        throttledRenderListenModal();
    }
}

function handleNewChatMessage(data) {
    rememberLiveUser(data);
    messageCount++;
    handleListenedMessage(data);
}

function clearHistories() {
    targetGiftHistory = [];
    pinnedCommentHistory = [];
    listenedMessages = [];
    listenedUserId = '';
    listenDraftValue = '';
    liveUsers.clear();
    renderActiveModal();
}

/** Rótulo curto para coluna Categoria (payload.category do servidor) */
function infractionCategoryLabel(category) {
    const map = {
        PROSELITISMO: 'Proselitismo Cristão',
        SPAM: 'Spam',
        GOLPE: 'Golpe',
        ODIO: 'Ataque Pessoal',
        OUTRO: 'Outro',
        REPETICAO: 'Repetição',
        CORRELACAO: 'Correlação Dino/Perfume'
    };
    const key = String(category || '').trim().toUpperCase();
    if (!key) return '—';
    return map[key] || key;
}

function createChart(ChartLib) {
    const ctx = chartCanvas.getContext('2d');
    return new ChartLib(ctx, {
        type: 'line',
        data: {
            labels: Array(60).fill('').map((_, index) => `${60 - index}s atrás`),
            datasets: [
                {
                    label: 'Mensagens/s',
                    data: chartData,
                    borderColor: '#fe2c55',
                    backgroundColor: 'rgba(254, 44, 85, 0.1)',
                    fill: true,
                    tension: 0.4
                },
                {
                    label: 'Presentes/s',
                    data: giftChartData,
                    borderColor: '#22c55e',
                    backgroundColor: 'rgba(34, 197, 94, 0.1)',
                    fill: true,
                    tension: 0.4
                }
            ]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            layout: {
                padding: 8
            },
            scales: {
                y: {
                    beginAtZero: true,
                    grid: {
                        color: 'rgba(255, 255, 255, 0.08)',
                        background: 'rgba(18, 21, 31, 0.6)',
                        drawBorder: false
                    },
                    ticks: {
                        stepSize: 1,
                        color: '#9aa0aa',
                        padding: 8
                    },
                    border: { display: false }
                },
                x: {
                    display: false,
                    grid: { display: false }
                }
            },
            plugins: {
                legend: {
                    display: true,
                    position: 'top',
                    labels: {
                        color: '#f2f3f5',
                        padding: 16,
                        usePointStyle: true,
                        boxWidth: 8
                    }
                }
            },
            animation: false
        }
    });
}

setInterval(() => {
    if (!chart) {
        return;
    }
    chartData.push(messageCount);
    chartData.shift();
    messageCount = 0;

    giftChartData.push(giftCount);
    giftChartData.shift();
    giftCount = 0;

    chart.update();
}, 1000);

targetGiftHistoryBtn.addEventListener('click', () => openHistoryModal('target-gifts'));
pinnedCommentHistoryBtn.addEventListener('click', () => openHistoryModal('pinned-comments'));
listenBtn.addEventListener('click', () => openHistoryModal('listen'));

if (refreshRankingBtn) {
    refreshRankingBtn.addEventListener('click', () => loadRanking());
}
if (generateReportBtn) {
    generateReportBtn.addEventListener('click', () => loadReport());
}

historyModalCloseBtn.addEventListener('click', closeHistoryModal);
historyModalBackdrop.addEventListener('click', event => {
    if (event.target === historyModalBackdrop) {
        closeHistoryModal();
    }
});

if (profileModalCloseBtn) {
    profileModalCloseBtn.addEventListener('click', closeProfileModal);
}
if (profileModalBackdrop) {
    profileModalBackdrop.addEventListener('click', event => {
        if (event.target === profileModalBackdrop) {
            closeProfileModal();
        }
    });
}
document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && activeModalType) {
        closeHistoryModal();
    }
});

{
    connectBtn.addEventListener('click', async () => {
        const username = usernameInput.value.trim().replace(/^@/, '');
        if (!username) {
            return;
        }

        setConnectingState();

        try {
            const response = await fetch('/api/connect', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ username })
            });

            if (!response.ok) {
                const payload = await response.json();
                throw new Error(payload.error || 'Falha ao conectar.');
            }
        } catch (error) {
            applyDisconnectedState(error.message);
        }
    });

    disconnectBtn.addEventListener('click', async () => {
        statusDiv.innerText = 'Desconectando...';

        try {
            await fetch('/api/disconnect', { method: 'POST' });
        } catch (error) {
            applyDisconnectedState(error.message);
        }
    });
}

const EXPIRATION_STORAGE_KEY = 'targetExpirationMinutes';

function loadTargetExpirationMinutes() {
    if (!targetExpirationMinutesInput) {
        return;
    }
    try {
        const stored = Number(localStorage.getItem(EXPIRATION_STORAGE_KEY));
        if (Number.isFinite(stored) && stored > 0) {
            targetExpirationMinutesInput.value = String(Math.floor(stored));
        }
    } catch {
        // ignore storage errors
    }
}

function persistTargetExpirationMinutes() {
    if (!targetExpirationMinutesInput) {
        return;
    }
    const minutes = getTargetExpirationMinutes();
    targetExpirationMinutesInput.value = String(minutes);
    try {
        localStorage.setItem(EXPIRATION_STORAGE_KEY, String(minutes));
    } catch {
        // ignore storage errors
    }
}

function onExpirationMinutesChanged(shouldPersist) {
    const minutes = Number(targetExpirationMinutesInput?.value);
    if (!Number.isFinite(minutes) || minutes <= 0) {
        return;
    }
    if (shouldPersist) {
        persistTargetExpirationMinutes();
    }
    resetTargetGiftTimers();
}

loadTargetExpirationMinutes();
if (targetExpirationMinutesInput) {
    targetExpirationMinutesInput.addEventListener('input', () => onExpirationMinutesChanged(false));
    targetExpirationMinutesInput.addEventListener('change', () => onExpirationMinutesChanged(true));
}

function setStatus(text, state) {
    statusDiv.innerText = text;
    statusDiv.classList.remove('connected', 'connecting', 'reconnecting', 'error');
    if (state) {
        statusDiv.classList.add(state);
    }
}

function setConnectingState() {
    connectBtn.disabled = true;
    disconnectBtn.disabled = true;
    setStatus('Conectando...', 'connecting');
}

function applyConnectedState(username) {
    setStatus(`Conectado a: ${username}`, 'connected');
    connectBtn.style.display = 'none';
    connectBtn.disabled = false;
    disconnectBtn.style.display = 'inline-block';
    disconnectBtn.disabled = false;
    usernameInput.disabled = true;
}

function applyDisconnectedState(error) {
    const isUserDisconnect = error === 'Desconectado pelo usuário' || error === 'Servidor encerrado';
    setStatus(isUserDisconnect ? 'Desconectado' : `Erro: ${error}`, isUserDisconnect ? '' : 'error');
    connectBtn.style.display = 'inline-block';
    connectBtn.disabled = false;
    disconnectBtn.style.display = 'none';
    disconnectBtn.disabled = false;
    usernameInput.disabled = false;
    clearTables();
}

function clearTables() {
    userTableBody.innerHTML = '';
    allGiftsTableBody.innerHTML = '';
    pinnedCommentsTableBody.innerHTML = '';
    if (correlationMessagesTableBody) {
        correlationMessagesTableBody.innerHTML = '';
    }

    for (const key in autoRemoveTimers) {
        clearTimeout(autoRemoveTimers[key]);
    }
    autoRemoveTimers = {};

    for (const key in pinnedCommentTimers) {
        clearTimeout(pinnedCommentTimers[key]);
    }
    pinnedCommentTimers = {};

    for (const key in flaggedMessageTimers) {
        clearTimeout(flaggedMessageTimers[key]);
    }
    flaggedMessageTimers = {};
    clearHistories();
    resetGoalDisplay();
}

function handleConnectionStatus(data) {
    console.log('[Frontend] handleConnectionStatus chamado:', data);
    if (data.success) {
        applyConnectedState(data.username);
        loadAvailableGifts();
        console.log('[Frontend] handleConnectionStatus: restaurando históricos...');
        loadAllGifts();
        loadPendingTargetGifts();
        loadPinnedComments();
        return;
    }

    // Reconexão automática em andamento: mantém o estado atual visível
    // sem limpar as tabelas.
    if (data.retries) {
        applyReconnectingState(data.retries, data.nextRetryInMs);
        return;
    }

    applyDisconnectedState(data.error || 'Falha ao conectar.');
}

function applyReconnectingState(retries, nextRetryInMs) {
    const secs = Math.max(0, Math.round((nextRetryInMs || 0) / 1000));
    setStatus(`Reconectando (tentativa ${retries}, em ${secs}s)...`, 'reconnecting');
    // Mantém os botões no estado conectado; o usuário ainda pode parar.
    connectBtn.style.display = 'none';
    disconnectBtn.style.display = 'inline-block';
    disconnectBtn.disabled = false;
}

function addUserToList(user, options = {}) {
    rememberLiveUser(user);
    if (!options.fromHistory) {
        addTargetGiftToHistory(user);
    }

    const historyId = user.historyId != null ? String(user.historyId) : '';

    if (historyId) {
        const existingByHistory = Array.from(userTableBody.querySelectorAll('.user-row')).find(row => {
            return row.dataset.historyId === historyId;
        });
        if (existingByHistory) {
            applyTargetGiftReceivedAt(existingByHistory, user.receivedAt, options.fromHistory);
            startAutoRemoveTimer(user.uniqueId, user.giftName, existingByHistory, {
                refreshStart: !options.fromHistory
            });
            return;
        }
    }

    const existingRow = Array.from(userTableBody.querySelectorAll('.user-row')).find(row => {
        return String(row.getAttribute('data-id')).toLowerCase() === String(user.uniqueId).toLowerCase() &&
            row.querySelector('.gift-name-cell').innerText === user.giftName;
    });

    if (existingRow) {
        if (!options.fromHistory) {
            const previousHistoryId = existingRow.dataset.historyId;
            if (previousHistoryId && previousHistoryId !== historyId) {
                markTargetGiftAnswered(previousHistoryId, 'automatic');
            }
        }
        if (historyId) {
            existingRow.dataset.historyId = historyId;
        }
        userTableBody.prepend(existingRow);
        if (user.isRed) {
            existingRow.classList.add('red');
        }
        applyTargetGiftReceivedAt(existingRow, user.receivedAt, options.fromHistory);
        startAutoRemoveTimer(user.uniqueId, user.giftName, existingRow, {
            refreshStart: !options.fromHistory
        });
        return;
    }

    const tr = document.createElement('tr');
    tr.className = 'user-row';
    tr.setAttribute('data-id', user.uniqueId);
    if (historyId) {
        tr.dataset.historyId = historyId;
    }

    if (user.isRed) {
        tr.classList.add('red');
    }

    const userTd = document.createElement('td');
    userTd.setAttribute('data-label', 'Usuário');
    const userSpan = document.createElement('span');
    userSpan.className = 'user-name';
    userSpan.textContent = user.nickname;
    if (user.uniqueId) {
        userSpan.style.cursor = 'pointer';
        userSpan.title = 'Ver perfil';
        userSpan.addEventListener('click', () => openProfile(user.uniqueId));
    }
    userTd.appendChild(userSpan);

    const badge = createFollowerBadge(followerStatusForDisplay(user));
    if (badge) {
        userTd.appendChild(badge);
    }

    tr.appendChild(userTd);

    const giftTd = document.createElement('td');
    giftTd.setAttribute('data-label', 'Presente');
    giftTd.className = 'gift-name-cell';
    giftTd.textContent = user.giftName;
    tr.appendChild(giftTd);

    const actionTd = document.createElement('td');
    actionTd.setAttribute('data-label', 'Ação');
    const actionBtn = document.createElement('button');
    actionBtn.className = 'action-btn';
    actionBtn.dataset.uniqueId = user.uniqueId;
    actionBtn.dataset.giftName = user.giftName;
    actionBtn.textContent = 'Respondido';
    actionBtn.addEventListener('click', event => {
        removeUser(event.currentTarget.dataset.uniqueId, event.currentTarget.dataset.giftName, event.currentTarget);
    });
    actionTd.appendChild(actionBtn);
    tr.appendChild(actionTd);

    applyTargetGiftReceivedAt(tr, user.receivedAt, options.fromHistory);
    userTableBody.prepend(tr);
    startAutoRemoveTimer(user.uniqueId, user.giftName, tr, {
        refreshStart: !options.fromHistory
    });
}

function applyTargetGiftReceivedAt(element, receivedAt, fromHistory) {
    if (!fromHistory || !receivedAt || element.dataset.addedAt) {
        return;
    }
    const ts = new Date(receivedAt).getTime();
    if (Number.isFinite(ts)) {
        element.dataset.addedAt = String(ts);
    }
}

function startAutoRemoveTimer(uniqueId, giftName, element, options = {}) {
    const refreshStart = options.refreshStart !== false;
    const timerKey = `${uniqueId}-${giftName}`;

    if (autoRemoveTimers[timerKey]) {
        clearTimeout(autoRemoveTimers[timerKey]);
        delete autoRemoveTimers[timerKey];
    }

    if (refreshStart || !element.dataset.addedAt) {
        element.dataset.addedAt = String(Date.now());
    }

    const addedAt = Number(element.dataset.addedAt) || Date.now();
    const remainingMs = getTargetExpirationMs() - (Date.now() - addedAt);

    if (remainingMs <= 0) {
        markTargetGiftAnswered(element.dataset.historyId, 'automatic');
        element.remove();
        if (activeModalType === 'target-gifts') {
            renderGiftHistory();
        }
        return;
    }

    autoRemoveTimers[timerKey] = setTimeout(() => {
        markTargetGiftAnswered(element.dataset.historyId, 'automatic');
        element.remove();
        delete autoRemoveTimers[timerKey];
        if (activeModalType === 'target-gifts') {
            renderGiftHistory();
        }
    }, remainingMs);
}

function getTargetExpirationMinutes() {
    const minutes = Number(targetExpirationMinutesInput?.value);
    return Number.isFinite(minutes) && minutes > 0 ? Math.floor(minutes) : 4;
}

function getTargetExpirationMs() {
    return getTargetExpirationMinutes() * 60 * 1000;
}

function resetTargetGiftTimers() {
    Array.from(userTableBody.querySelectorAll('.user-row')).forEach(row => {
        const uniqueId = row.getAttribute('data-id');
        const giftName = row.querySelector('.gift-name-cell')?.innerText;
        if (uniqueId && giftName) {
            // Mantém o horário original do presente e só reaplica o prazo atual.
            startAutoRemoveTimer(uniqueId, giftName, row, { refreshStart: false });
        }
    });
}

function normalizeUserIdForGift(uniqueId) {
    return String(uniqueId || '').toLowerCase();
}

function normalizedGiftNameInTable(row) {
    return (row.querySelector('.gift-name-cell')?.innerText || '').trim().toLowerCase();
}

function normalizedGiftNameFromPayload(gift) {
    return String(gift.giftName || '').trim().toLowerCase();
}

function findAllGiftsRowForGift(gift) {
    const uid = normalizeUserIdForGift(gift.uniqueId);
    const giftId = gift.giftId != null && gift.giftId !== '' ? String(gift.giftId) : '';
    const name = normalizedGiftNameFromPayload(gift);
    return Array.from(allGiftsTableBody.querySelectorAll('tr')).find(row => {
        if (normalizeUserIdForGift(row.getAttribute('data-user-id')) !== uid) {
            return false;
        }
        const rowGiftId = row.getAttribute('data-gift-id') || '';
        if (giftId && rowGiftId) {
            return rowGiftId === giftId;
        }
        return normalizedGiftNameInTable(row) === name;
    });
}

function getGiftCountFromTableRow(row) {
    const cell = row.querySelector('.gift-count-cell');
    if (!cell) {
        return 0;
    }
    const n = parseInt(String(cell.textContent).trim(), 10);
    return Number.isFinite(n) && n >= 0 ? n : 0;
}

function isGiftStreakInProgress(gift) {
    const v = gift ? gift.repeatEnd : undefined;
    return v === false || v === 0 || v === 'false' || v === '0';
}

function committedGiftCountFromRow(row) {
    const raw = row.getAttribute('data-committed');
    if (raw == null || raw === '') {
        return getGiftCountFromTableRow(row);
    }
    const n = Number(raw);
    return Number.isFinite(n) && n >= 0 ? n : 0;
}

function reorderAllGiftsTableByCount() {
    const rows = Array.from(allGiftsTableBody.children);
    rows.sort((a, b) => (Number(b.getAttribute('data-count')) || 0) - (Number(a.getAttribute('data-count')) || 0));
    rows.forEach(row => allGiftsTableBody.appendChild(row));
}

function trimAllGiftsTable(maxRows) {
    while (allGiftsTableBody.children.length > maxRows) {
        allGiftsTableBody.lastElementChild.remove();
    }
}

function applyGiftFilter() {
    if (!giftSearchInput) return;
    const filterText = giftSearchInput.value.trim().toLowerCase();
    const rows = Array.from(allGiftsTableBody.querySelectorAll('tr'));
    
    rows.forEach(row => {
        const giftName = (row.getAttribute('data-gift-name') || '').toLowerCase();
        if (!filterText || giftName.includes(filterText)) {
            row.style.display = '';
        } else {
            row.style.display = 'none';
        }
    });
}

if (giftSearchInput) {
    giftSearchInput.addEventListener('input', applyGiftFilter);
}

function addAllGiftToList(gift) {
    giftCount++;
    rememberLiveUser(gift);

    const quantity = Math.max(1, Number(gift.repeatCount) || 1);
    const inProgress = isGiftStreakInProgress(gift);
    const existingRow = findAllGiftsRowForGift(gift);

    if (existingRow) {
        const committed = committedGiftCountFromRow(existingRow);
        // Combo em andamento: mostra committed + repeatCount. No fim, soma só o combo (não o total já exibido).
        const nextCommitted = inProgress ? committed : committed + quantity;
        const nextPending = inProgress ? quantity : 0;
        const next = nextCommitted + nextPending;
        existingRow.setAttribute('data-committed', String(nextCommitted));
        existingRow.setAttribute('data-count', String(next));
        const countCell = existingRow.querySelector('.gift-count-cell');
        if (countCell) {
            countCell.textContent = String(next);
        }
        if (gift.isRed) {
            existingRow.classList.add('red');
        }
        ensureFollowerBadge(existingRow.querySelector('td'), gift);
        reorderAllGiftsTableByCount();
        trimAllGiftsTable(200);
        applyGiftFilter();
        return;
    }

    const committed = inProgress ? 0 : quantity;
    const pending = inProgress ? quantity : 0;
    const total = committed + pending;

    const tr = document.createElement('tr');
    tr.className = 'gift-row';
    tr.setAttribute('data-id', gift.uniqueId);
    tr.setAttribute('data-user-id', gift.uniqueId);
    tr.setAttribute('data-gift-id', gift.giftId != null && gift.giftId !== '' ? String(gift.giftId) : '');
    tr.setAttribute('data-gift-name', gift.giftName || '');
    tr.setAttribute('data-committed', String(committed));
    tr.setAttribute('data-count', String(total));
    tr.setAttribute('data-target-gift', gift.isTargetGift ? 'true' : 'false');

    if (gift.isRed) {
        tr.classList.add('red');
    }

    const userTd = document.createElement('td');
    userTd.setAttribute('data-label', 'Usuário');
    const userSpan = document.createElement('span');
    userSpan.className = 'user-name';
    userSpan.textContent = gift.nickname;
    if (gift.uniqueId) {
        userSpan.style.cursor = 'pointer';
        userSpan.title = 'Ver perfil';
        userSpan.addEventListener('click', () => openProfile(gift.uniqueId));
    }
    userTd.appendChild(userSpan);

    const badge = createFollowerBadge(followerStatusForDisplay(gift));
    if (badge) {
        userTd.appendChild(badge);
    }
    tr.appendChild(userTd);

    const giftTd = document.createElement('td');
    giftTd.setAttribute('data-label', 'Presente');
    giftTd.className = 'gift-name-cell';
    giftTd.textContent = gift.giftName;
    tr.appendChild(giftTd);

    const countTd = document.createElement('td');
    countTd.setAttribute('data-label', 'Qtd');
    countTd.className = 'gift-count-cell';
    countTd.textContent = String(total);
    tr.appendChild(countTd);

    allGiftsTableBody.appendChild(tr);
    reorderAllGiftsTableByCount();
    trimAllGiftsTable(200);
    applyGiftFilter();
}

function pinnedCommentKey(pinnedComment) {
    if (pinnedComment.pinId) {
        return `pin:${pinnedComment.pinId}`;
    }
    if (pinnedComment.id) {
        return `id:${pinnedComment.id}`;
    }
    return `${String(pinnedComment.uniqueId || '').toLowerCase()}|${pinnedComment.comment || ''}|${pinnedComment.timestamp || ''}`;
}

function addPinnedCommentToList(pinnedComment, options = {}) {
    rememberLiveUser(pinnedComment);
    if (!options.fromHistory) {
        addPinnedCommentToHistory();
    }

    const key = pinnedCommentKey(pinnedComment);
    const existing = Array.from(pinnedCommentsTableBody.querySelectorAll('.pinned-comment-row')).find(row => {
        return row.dataset.pinKey === key;
    });
    if (existing) {
        return;
    }

    const timerKey = `${pinnedComment.pinId || pinnedComment.timestamp || Date.now()}-${Math.random()}`;
    const tr = document.createElement('tr');
    tr.className = 'pinned-comment-row';
    tr.setAttribute('data-id', pinnedComment.uniqueId || '');
    tr.dataset.pinKey = key;

    const userTd = document.createElement('td');
    userTd.setAttribute('data-label', 'Usuário');
    const userSpan = document.createElement('span');
    userSpan.className = 'user-name';
    userSpan.innerText = pinnedComment.nickname || pinnedComment.uniqueId || 'Nao identificado';
    if (pinnedComment.uniqueId) {
        userSpan.style.cursor = 'pointer';
        userSpan.title = 'Ver perfil';
        userSpan.addEventListener('click', () => openProfile(pinnedComment.uniqueId));
    }
    userTd.appendChild(userSpan);

    const badge = createFollowerBadge(followerStatusForDisplay(pinnedComment));
    if (badge) {
        userTd.appendChild(badge);
    }

    const commentTd = document.createElement('td');
    commentTd.setAttribute('data-label', 'Comentário');
    commentTd.className = 'comment-cell';
    commentTd.innerText = pinnedComment.comment || '[sem texto identificado]';

    tr.appendChild(userTd);
    tr.appendChild(commentTd);
    pinnedCommentsTableBody.prepend(tr);

    if (!options.fromHistory) {
        pinnedCommentTimers[timerKey] = setTimeout(() => {
            tr.remove();
            delete pinnedCommentTimers[timerKey];
        }, 50 * 1000);
    }

    if (pinnedCommentsTableBody.children.length > 50) {
        pinnedCommentsTableBody.lastChild.remove();
    }
}

function addFlaggedMessageToList(data) {
    if (!correlationMessagesTableBody) {
        return;
    }

    rememberLiveUser(data);

    const category = String(data.category || '').toUpperCase();
    if (!['REPETICAO', 'CORRELACAO', 'SPAM', 'GOLPE', 'PROSELITISMO', 'ODIO', 'OUTRO'].includes(category)) {
        return;
    }

    const messageKey = `alert-${category}-${String(data.uniqueId || '').toLowerCase()}-${String(data.comment || '').toLowerCase()}`;
    const existingRow = Array.from(correlationMessagesTableBody.children).find(row => row.dataset.messageKey === messageKey);
    if (existingRow) {
        existingRow.classList.add('blink-row');
        setTimeout(() => existingRow.classList.remove('blink-row'), 2000);
        return;
    }

    const timerKey = `flagged-${Date.now()}-${Math.random()}`;
    const tr = document.createElement('tr');
    tr.className = 'flagged-message-row blink-row';
    tr.dataset.messageKey = messageKey;

    const tdUser = document.createElement('td');
    tdUser.setAttribute('data-label', 'Usuário');
    const spanUser = document.createElement('span');
    spanUser.className = 'user-name';
    spanUser.textContent = data.nickname != null ? String(data.nickname) : '';
    if (data.uniqueId) {
        spanUser.style.cursor = 'pointer';
        spanUser.title = 'Ver perfil';
        spanUser.addEventListener('click', () => openProfile(data.uniqueId));
    }
    tdUser.appendChild(spanUser);

    const badge = createFollowerBadge(followerStatusForDisplay(data));
    if (badge) {
        tdUser.appendChild(badge);
    }

    const tdMsg = document.createElement('td');
    tdMsg.setAttribute('data-label', 'Mensagem');
    tdMsg.className = 'comment-cell';
    tdMsg.textContent = data.comment != null ? String(data.comment) : '';

    const tdCat = document.createElement('td');
    tdCat.setAttribute('data-label', 'Tipo');
    const spanCat = document.createElement('span');
    spanCat.className = 'infraction-category';
    spanCat.textContent = infractionCategoryLabel(category);
    if (category) spanCat.title = category;
    tdCat.appendChild(spanCat);

    const tdReason = document.createElement('td');
    tdReason.setAttribute('data-label', 'Detalhe');
    tdReason.textContent = data.reason != null ? String(data.reason) : '';

    tr.appendChild(tdUser);
    tr.appendChild(tdMsg);
    tr.appendChild(tdCat);
    tr.appendChild(tdReason);

    correlationMessagesTableBody.prepend(tr);

    flaggedMessageTimers[timerKey] = setTimeout(() => {
        tr.remove();
        delete flaggedMessageTimers[timerKey];
    }, 60 * 1000);

    if (correlationMessagesTableBody.children.length > 50) {
        correlationMessagesTableBody.lastChild.remove();
    }
}

function handleKeywordMention(data) {
    if (!data) {
        return;
    }

    rememberLiveUser(data);
    markUserRed(data.uniqueId || '');

    addPinnedCommentToList({
        uniqueId: data.uniqueId,
        nickname: data.nickname,
        isFollower: data.isFollower,
        comment: data.comment,
        pinId: `keyword-${data.keyword || 'target'}-${data.uniqueId || 'anon'}-${data.timestamp || Date.now()}`,
        timestamp: data.timestamp || Date.now()
    });
}

function addCorrelationMessageToList(data) {
    if (!correlationMessagesTableBody) {
        return;
    }

    const correlationId = String(data.correlationId || '').trim();
    if (correlationId) {
        const existing = Array.from(correlationMessagesTableBody.children).find((row) => row.dataset.correlationId === correlationId);
        if (existing) {
            existing.remove();
        }
    }

    const tr = document.createElement('tr');
    tr.className = 'flagged-message-row';
    if (correlationId) {
        tr.dataset.correlationId = correlationId;
    }
    if (data.replacement) {
        tr.classList.add('blink-row');
        setTimeout(() => tr.classList.remove('blink-row'), 1800);
    }

    const tdGiftUser = document.createElement('td');
    tdGiftUser.setAttribute('data-label', 'Usuário');
    const spanGiftUser = document.createElement('span');
    spanGiftUser.className = 'user-name';
    const userLabel = data.giftNickname || data.giftUserId || 'Nao identificado';
    spanGiftUser.textContent = data.giftUserId
        ? `${userLabel} (@${data.giftUserId})`
        : userLabel;
    tdGiftUser.appendChild(spanGiftUser);

    const tdQuestion = document.createElement('td');
    tdQuestion.setAttribute('data-label', 'Mensagem');
    tdQuestion.className = 'comment-cell';
    tdQuestion.textContent = data.question || '[pergunta não encontrada]';

    const tdConfidence = document.createElement('td');
    tdConfidence.setAttribute('data-label', 'Tipo');
    const confidenceBadge = document.createElement('span');
    confidenceBadge.className = 'infraction-category';
    confidenceBadge.textContent = String(data.confidence || 'medium').toUpperCase();
    tdConfidence.appendChild(confidenceBadge);

    const tdMethod = document.createElement('td');
    tdMethod.setAttribute('data-label', 'Detalhe');
    const methodLabel = String(data.method || 'heuristica');
    tdMethod.textContent = data.replacement ? `${methodLabel} (ajustada)` : methodLabel;

    tr.appendChild(tdGiftUser);
    tr.appendChild(tdQuestion);
    tr.appendChild(tdConfidence);
    tr.appendChild(tdMethod);

    correlationMessagesTableBody.prepend(tr);
    if (correlationMessagesTableBody.children.length > 50) {
        correlationMessagesTableBody.lastChild.remove();
    }
}

function markUserRed(uniqueId) {
    const targetId = String(uniqueId).toLowerCase();
    const targetRows = document.querySelectorAll('.user-row, .gift-row[data-target-gift="true"]');

    targetRows.forEach(row => {
        const rowId = String(row.getAttribute('data-id')).toLowerCase();
        if (rowId === targetId) {
            row.classList.add('red');
        }
    });
}

function removeUser(uniqueId, giftName, button) {
    const timerKey = `${uniqueId}-${giftName}`;
    if (autoRemoveTimers[timerKey]) {
        clearTimeout(autoRemoveTimers[timerKey]);
        delete autoRemoveTimers[timerKey];
    }

    const tr = button.closest('.user-row');
    if (tr) {
        markTargetGiftAnswered(tr.dataset.historyId, 'manual');
        tr.remove();
        if (activeModalType === 'target-gifts') {
            renderGiftHistory();
        }
    }
}

async function loadInitialState() {
    try {
        const response = await fetch('/api/state');
        const payload = await response.json();

        if (payload.connected && payload.username) {
            console.log('[Frontend] loadInitialState: já conectado a', payload.username, '- carregando presentes...');
            usernameInput.value = payload.username;
            applyConnectedState(payload.username);
            await Promise.all([
                loadAllGifts(),
                loadPendingTargetGifts(),
                loadPinnedComments(),
                loadRanking()
            ]);
        }

        // Carrega ranking e metas mesmo desconectado (vazio se não houver live).
        loadRanking();
        loadGoals();
    } catch (error) {
        setStatus('Servidor indisponível', 'error');
    }
}

function setupEventStream() {
    const eventSource = new EventSource('/events');

    eventSource.addEventListener('server-state', event => {
        const data = JSON.parse(event.data);
        if (data.connected && data.username) {
            usernameInput.value = data.username;
            applyConnectedState(data.username);
            loadGoals();
        } else {
            applyDisconnectedState('Desconectado pelo usuário');
        }
    });

    eventSource.addEventListener('connection-status', event => {
        handleConnectionStatus(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-chat-message', event => {
        handleNewChatMessage(JSON.parse(event.data));
    });

    eventSource.addEventListener('live-user-connected', event => {
        rememberLiveUser(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-follower', event => {
        rememberLiveUser(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-social-event', event => {
        rememberLiveUser(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-gift-user', event => {
        try {
            addUserToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar presente alvo:', error, event.data);
        }
    });

    eventSource.addEventListener('any-gift-received', event => {
        try {
            addAllGiftToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar presente:', error, event.data);
        }
    });

    eventSource.addEventListener('gifts-list', event => {
        const data = JSON.parse(event.data);
        populateAvailableGifts(data.gifts || data);
    });

    eventSource.addEventListener('pinned-comment', event => {
        try {
            addPinnedCommentToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar comentário fixado:', error, event.data);
        }
    });

    eventSource.addEventListener('flagged-message', event => {
        try {
            addFlaggedMessageToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar alerta:', error, event.data);
        }
    });

    eventSource.addEventListener('gift-question-correlation', event => {
        try {
            addCorrelationMessageToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar correlação:', error, event.data);
        }
    });

    eventSource.addEventListener('keyword-mention', event => {
        handleKeywordMention(JSON.parse(event.data));
    });

    eventSource.addEventListener('mark-user-red', event => {
        markUserRed(JSON.parse(event.data));
    });

    eventSource.addEventListener('settings-update', event => {
        renderTargetGifts();
    });

    eventSource.addEventListener('goal-update', event => {
        try {
            const data = JSON.parse(event.data);
            if (!data.progress) return;
            const card = findGoalCard(data.progress.goal && data.progress.goal.id);
            if (card) updateGoalCard(card, data.progress);
            else loadGoals();
        } catch (error) {
            console.error('[Frontend] Falha ao processar goal-update:', error);
        }
    });

    eventSource.addEventListener('goal-unlocked', event => {
        try {
            const data = JSON.parse(event.data);
            if (data.progress) {
                const card = findGoalCard(data.progress.goal && data.progress.goal.id);
                if (card) updateGoalCard(card, data.progress);
                else loadGoals();
            }
            const unlocked = data.unlockedMilestones || [];
            if (unlocked.length > 0) {
                const percent = data.progress ? Math.round(data.progress.percent) : 0;
                const reward = unlocked[0].reward ? translateGiftName(unlocked[0].reward) : 'recompensa';
                showGoalToast(`🎉 ${percent}% — recompensa: ${reward}`);
            }
        } catch (error) {
            console.error('[Frontend] Falha ao processar goal-unlocked:', error);
        }
    });

    eventSource.addEventListener('goal-completed', event => {
        try {
            const data = JSON.parse(event.data);
            if (!data.progress) return;
            showGoalToast(`🎉 Meta "${data.progress.goal.title}" concluída!`);
            loadGoals();
        } catch (error) {
            console.error('[Frontend] Falha ao processar goal-completed:', error);
        }
    });

    eventSource.onerror = () => {
        setStatus('Reconectando ao servidor...', 'reconnecting');
    };
}

// --- Ranking Inteligente ---
function riskBadgeClass(level) {
    const map = {
        'none': 'risk-none',
        'low': 'risk-low',
        'medium': 'risk-medium',
        'high': 'risk-high',
        'critical': 'risk-critical'
    };
    return map[level] || 'risk-none';
}

function riskLabel(level) {
    const map = {
        'none': 'Nenhum',
        'low': 'Baixo',
        'medium': 'Médio',
        'high': 'Alto',
        'critical': 'Crítico'
    };
    return map[level] || (level || 'Nenhum');
}

async function loadRanking() {
    if (!rankingTableBody) return;
    try {
        const response = await fetch('/api/ranking');
        const data = await response.json();
        renderRanking(data);
    } catch (error) {
        console.error('[Frontend] Falha ao carregar ranking:', error);
    }
}

function renderRanking(ranking) {
    if (!rankingTableBody) return;
    rankingTableBody.innerHTML = '';
    const rows = ranking.userRanks || ranking.userRanks || [];
    if (!rows.length) {
        const tr = document.createElement('tr');
        const td = document.createElement('td');
        td.colSpan = 8;
        td.style.textAlign = 'center';
        td.style.color = 'var(--text-muted)';
        td.textContent = 'Sem dados de engajamento ainda.';
        tr.appendChild(td);
        rankingTableBody.appendChild(tr);
        return;
    }
    rows.forEach((user, index) => {
        const tr = document.createElement('tr');
        tr.className = 'user-row';
        tr.dataset.uniqueId = user.uniqueId || '';

        const tdRank = document.createElement('td');
        tdRank.setAttribute('data-label', '#');
        tdRank.className = 'ranking-rank';
        tdRank.textContent = String(index + 1);
        tr.appendChild(tdRank);

        const tdUser = document.createElement('td');
        tdUser.setAttribute('data-label', 'Usuário');
        const spanUser = document.createElement('span');
        spanUser.className = 'user-name';
        spanUser.style.cursor = 'pointer';
        spanUser.textContent = (user.nickname || user.uniqueId || 'Nao identificado');
        spanUser.addEventListener('click', () => {
            if (user.uniqueId) {
                openProfile(user.uniqueId);
            }
        });
        tdUser.appendChild(spanUser);
        tr.appendChild(tdUser);

        const tdScore = document.createElement('td');
        tdScore.setAttribute('data-label', 'Score');
        tdScore.textContent = (user.score != null ? user.score.toFixed(1) : '0');
        tr.appendChild(tdScore);

        const tdGifts = document.createElement('td');
        tdGifts.setAttribute('data-label', 'Presentes');
        tdGifts.textContent = String(user.giftCount || 0);
        tr.appendChild(tdGifts);

        const tdShares = document.createElement('td');
        tdShares.setAttribute('data-label', 'Compart.');
        tdShares.textContent = String(user.shareCount || 0);
        tr.appendChild(tdShares);

        const tdMessages = document.createElement('td');
        tdMessages.setAttribute('data-label', 'Mensagens');
        tdMessages.textContent = String(user.messageCount || 0);
        tr.appendChild(tdMessages);

        const tdQuestions = document.createElement('td');
        tdQuestions.setAttribute('data-label', 'Perguntas');
        tdQuestions.textContent = String(user.questionCount || 0);
        tr.appendChild(tdQuestions);

        const tdLikes = document.createElement('td');
        tdLikes.setAttribute('data-label', 'Curtidas');
        tdLikes.textContent = String(user.likeCount || 0);
        tr.appendChild(tdLikes);

        rankingTableBody.appendChild(tr);
    });

    // Total de curtidas da sala (contador acumulado reportado pelo stream).
    if (ranking.totalLikes > 0) {
        const trTotal = document.createElement('tr');
        trTotal.style.fontWeight = '600';
        const tdLabel = document.createElement('td');
        tdLabel.colSpan = 7;
        tdLabel.style.textAlign = 'right';
        tdLabel.textContent = 'Total de curtidas da live:';
        const tdValue = document.createElement('td');
        tdValue.textContent = String(ranking.totalLikes);
        trTotal.appendChild(tdLabel);
        trTotal.appendChild(tdValue);
        rankingTableBody.appendChild(trTotal);
    }
}

// --- Relatório Pós-Live ---
async function loadReport() {
    if (!generateReportBtn) return;
    generateReportBtn.disabled = true;
    const originalText = generateReportBtn.textContent;
    generateReportBtn.textContent = 'Gerando...';
    reportWrap.style.display = 'block';
    reportError.style.display = 'none';
    reportSummary.innerHTML = '';
    reportText.textContent = 'Gerando relatório, aguarde...';
    try {
        const response = await fetch('/api/report');
        const data = await response.json();
        if (data.error) {
            reportText.textContent = '';
            reportError.textContent = 'Erro: ' + data.error;
            reportError.style.display = 'block';
        } else {
            renderReport(data);
        }
    } catch (error) {
        reportText.textContent = '';
        reportError.textContent = 'Falha ao conectar com o servidor.';
        reportError.style.display = 'block';
        console.error('[Frontend] Falha ao gerar relatório:', error);
    } finally {
        generateReportBtn.disabled = false;
        generateReportBtn.textContent = originalText;
    }
}

function renderReport(report) {
    if (!reportSummary) return;
    reportSummary.innerHTML = '';
    const stats = [
        { value: report.durationMinutes != null ? report.durationMinutes + ' min' : '—', label: 'Duração' },
        { value: report.messageCount || 0, label: 'Mensagens' },
        { value: report.participantCount || 0, label: 'Participantes' },
        { value: report.giftCount || 0, label: 'Presentes' },
        { value: report.giftTotal || 0, label: 'Total presentes' }
    ];
    stats.forEach(stat => {
        const box = document.createElement('div');
        box.className = 'report-stat';
        box.innerHTML = '<div class="stat-value">' + escapeHtml(String(stat.value)) + '</div><div class="stat-label">' + escapeHtml(stat.label) + '</div>';
        reportSummary.appendChild(box);
    });
    reportText.textContent = report.summary || 'Relatório indisponível.';
}

function escapeHtml(value) {
    return String(value)
        .replace(/&/g, '&')
        .replace(/</g, '<')
        .replace(/>/g, '>');
}

function updateAllGiftsVisibility() {
    if (!allGiftsSection || !allGiftsTableContainer) return;
    // Sempre manter a tabela de todos os presentes visível,
    // mesmo quando há presentes alvos configurados.
    allGiftsSection.style.display = '';
    allGiftsTableContainer.style.display = '';
}

function renderTargetGifts() {
    if (!targetGiftsList) return;
    targetGiftsList.innerHTML = '';

    fetch('/api/settings')
        .then(r => r.json())
        .then(settings => {
            const gifts = settings.targetGifts || [];
            gifts.forEach(giftName => {
                const span = document.createElement('span');
                span.className = 'target-gift-chip';
                const label = document.createElement('span');
                label.textContent = giftName;
                span.appendChild(label);
                const btn = document.createElement('button');
                btn.type = 'button';
                btn.textContent = '×';
                btn.setAttribute('aria-label', `Remover ${giftName}`);
                btn.addEventListener('click', () => removeTargetGift(giftName));
                span.appendChild(btn);
                targetGiftsList.appendChild(span);
            });
            updateAllGiftsVisibility();
        })
        .catch(() => {});
}

async function removeTargetGift(giftToRemove) {
    console.log('Removing target gift:', giftToRemove);
    try {
        const response = await fetch('/api/settings');
        const settings = await response.json();
        const gifts = settings.targetGifts || [];
        const updatedGifts = gifts.filter(g => g !== giftToRemove);

        const res = await fetch('/api/settings', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ...settings, targetGifts: updatedGifts })
        });
        if (res.ok) {
            console.log('Successfully removed target gift:', giftToRemove);
            renderTargetGifts();
        } else {
            console.error('Failed to remove target gift:', await res.text());
        }
    } catch (e) {
        console.error('Erro ao remover presente alvo:', e);
    }
}

async function loadAvailableGifts() {
    if (!availableGiftSelect) return;
    try {
        const response = await fetch('/api/available-gifts');
        if (!response.ok) return;
        const gifts = await response.json();
        populateAvailableGifts(gifts);
    } catch (e) {
        console.error('Erro ao carregar presentes disponíveis:', e);
    }
}

function setSelectValue(el, value) {
    const target = value ? String(value) : '';
    el.querySelectorAll('option[data-dynamic]').forEach(o => o.remove());
    if (target && !el.querySelector(`option[value="${CSS.escape(target)}"]`)) {
        const option = document.createElement('option');
        option.value = target;
        option.textContent = translateGiftName(target);
        option.dataset.dynamic = '1';
        el.appendChild(option);
    }
    el.value = target;
}

function populateAvailableGifts(gifts) {
    if (!Array.isArray(gifts) || gifts.length === 0) {
        return;
    }
    const unique = [...new Set(gifts.map(gift => String(gift || '').trim()).filter(Boolean))];
    unique.sort((a, b) => a.localeCompare(b, 'pt'));
    // O dropdown de presentes alvos, o de metas da live e os de recompensa dos
    // milestones compartilham as mesmas opções; apenas o placeholder difere.
    const selects = [
        { el: availableGiftSelect, placeholder: 'Selecione um presente...' },
        { el: goalGiftSelect, placeholder: 'Todos os presentes' },
        ...goalMilestoneRows.map(row => ({ el: row.querySelector('.goal-ms-reward'), placeholder: 'Recompensa' }))
    ];
    for (const { el, placeholder } of selects) {
        if (!el) continue;
        const current = el.value;
        el.innerHTML = `<option value="">${placeholder}</option>`;
        unique.forEach(gift => {
            const option = document.createElement('option');
            // value mantém o nome original (ING) para o backend reconhecer o presente na live;
            // a exibição é traduzida para PT-BR, mas o nome original é mantido entre parênteses:
            // a busca por digitação do <select> só procura no texto visível, então assim
            // encontra o presente pelo nome PT-BR OU inglês (ex.: digitar "soccer" acha
            // "Bola de Futebol (Soccer Ball)").
            const label = translateGiftName(gift);
            option.value = gift;
            option.textContent = label !== gift ? `${label} (${gift})` : label;
            el.appendChild(option);
        });
        if (current) {
            setSelectValue(el, current);
        }
    }
}

// Carrega presentes históricos do banco no frontend (pós-reconexão ou estado inicial).
async function loadAllGifts() {
    if (!allGiftsTableBody) {
        console.error('[Frontend] loadAllGifts: allGiftsTableBody não encontrado.');
        return;
    }
    if (allGiftsTableBody.children.length > 0) {
        return;
    }
    try {
        console.log('[Frontend] loadAllGifts: buscando presentes...');
        const response = await fetch('/api/gifts');
        console.log(`[Frontend] loadAllGifts: status=${response.status}`);
        if (!response.ok) {
            console.error('[Frontend] loadAllGifts: response não ok');
            return;
        }
        const gifts = await response.json();
        if (!Array.isArray(gifts)) {
            console.error('[Frontend] loadAllGifts: payload inválido', gifts);
            return;
        }
        if (allGiftsTableBody.children.length > 0) {
            return;
        }
        console.log(`[Frontend] loadAllGifts: ${gifts.length} presentes recebidos. Exemplo:`, gifts[0]);
        allGiftsTableBody.innerHTML = '';
        gifts.forEach(gift => {
            try {
                addAllGiftToList(gift);
            } catch (e) {
                console.error('[Frontend] loadAllGifts: erro ao adicionar gift:', e, gift);
            }
        });
        console.log(`[Frontend] loadAllGifts: ${gifts.length} presentes renderizados.`);
    } catch (e) {
        console.error('[Frontend] loadAllGifts: erro:', e);
    }
}

async function loadPendingTargetGifts() {
    if (!userTableBody) {
        return;
    }
    try {
        const response = await fetch('/api/target-gift-history?pending=1&limit=50');
        if (!response.ok) {
            console.error('[Frontend] loadPendingTargetGifts: status', response.status);
            return;
        }
        const items = await response.json();
        if (!Array.isArray(items)) {
            return;
        }
        items.slice().reverse().forEach(item => {
            addUserToList({
                uniqueId: item.uniqueId,
                nickname: item.nickname,
                giftName: item.giftName,
                historyId: item.id,
                receivedAt: item.receivedAt
            }, { fromHistory: true });
        });
        console.log(`[Frontend] loadPendingTargetGifts: ${items.length} pendentes restaurados.`);
    } catch (e) {
        console.error('[Frontend] loadPendingTargetGifts: erro:', e);
    }
}

async function loadPinnedCommentsFromApi() {
    try {
        const response = await fetch('/api/pinned-comments?limit=50');
        if (!response.ok) {
            throw new Error(`status ${response.status}`);
        }
        const items = await response.json();
        return Array.isArray(items) ? items : [];
    } catch (error) {
        console.error('[Frontend] Falha ao carregar histórico de comentários fixados:', error);
        return [];
    }
}

async function loadPinnedComments() {
    if (!pinnedCommentsTableBody) {
        return;
    }
    try {
        const items = await loadPinnedCommentsFromApi();
        items.slice().reverse().forEach(item => {
            addPinnedCommentToList(item, { fromHistory: true });
        });
        console.log(`[Frontend] loadPinnedComments: ${items.length} comentários restaurados.`);
    } catch (e) {
        console.error('[Frontend] loadPinnedComments: erro:', e);
    }
}

async function addTargetGift() {
    if (isAddingTargetGift) return;
    isAddingTargetGift = true;
    try {
        const value = availableGiftSelect.value.trim();
        if (!value) return;
        console.log('Adding target gift:', value);

        const response = await fetch('/api/settings');
        const settings = await response.json();
        const gifts = settings.targetGifts || [];
        if (gifts.includes(value)) {
            console.log('Gift already exists in targets:', value);
            return;
        }

        const updatedGifts = [...gifts, value];
        const res = await fetch('/api/settings', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ...settings, targetGifts: updatedGifts })
        });
        if (res.ok) {
            console.log('Successfully added target gift:', value);
            availableGiftSelect.value = '';
            // renderTargetGifts() is triggered by the SSE 'settings-update' event
            // sent by the server after the POST, avoiding a race that duplicates tags.
        } else {
            console.error('Failed to add target gift:', await res.text());
        }
    } catch (e) {
        console.error('Erro ao adicionar presente alvo:', e);
    } finally {
        isAddingTargetGift = false;
    }
}

addTargetGiftBtn.addEventListener('click', addTargetGift);

// --- Metas da Live ---

let currentGoalId = 0;
let goalToastTimer = null;

function showGoalToast(message) {
    let toast = document.getElementById('goalToast');
    if (!toast) {
        toast = document.createElement('div');
        toast.id = 'goalToast';
        toast.className = 'goal-toast';
        document.body.appendChild(toast);
    }
    toast.textContent = message;
    toast.classList.add('show');
    if (goalToastTimer) clearTimeout(goalToastTimer);
    goalToastTimer = setTimeout(() => toast.classList.remove('show'), 6000);
}

function updateGoalButtons() {
    if (goalSaveBtn) goalSaveBtn.textContent = currentGoalId ? 'Atualizar' : 'Salvar';
    if (goalResetBtn) goalResetBtn.hidden = !currentGoalId;
}

function goalUnitsText(goal, units) {
    const giftName = (goal && goal.giftName) || '';
    const target = (goal && goal.targetUnits) || 0;
    return giftName
        ? `${units} / ${target} ${translateGiftName(giftName)}`
        : `${units} / ${target} unidades`;
}

function buildMilestoneRow(m) {
    const row = document.createElement('div');
    row.className = 'goal-milestone' + (m.unlocked ? ' unlocked' : '');
    const units = document.createElement('span');
    units.className = 'goal-ms-units-label';
    units.textContent = `${m.atUnits} unidades`;
    const reward = document.createElement('span');
    reward.className = 'goal-ms-reward-label';
    reward.textContent = m.reward ? translateGiftName(m.reward) : '—';
    const badge = document.createElement('span');
    badge.className = 'goal-ms-badge' + (m.unlocked ? ' on' : '');
    badge.textContent = m.unlocked ? '🎉 Desbloqueado' : '🔒 Pendente';
    row.append(units, reward, badge);
    return row;
}

function buildGoalCard(progress) {
    const goal = progress.goal;
    const pct = Math.max(0, Math.min(100, progress.percent || 0));
    const card = document.createElement('div');
    card.className = 'goal-active-card';
    card.dataset.goalId = String(goal.id);

    const head = document.createElement('div');
    head.className = 'goal-active-head';
    const title = document.createElement('span');
    title.className = 'goal-active-title';
    title.textContent = goal.giftName
        ? `${goal.title} — ${translateGiftName(goal.giftName)}`
        : goal.title;
    title.title = 'Clique para editar';
    title.addEventListener('click', () => fillGoalForm(goal));
    const actions = document.createElement('div');
    actions.className = 'goal-active-actions';
    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'small-btn';
    cancelBtn.type = 'button';
    cancelBtn.textContent = 'Cancelar';
    cancelBtn.addEventListener('click', e => { e.stopPropagation(); cancelGoal(goal.id); });
    const completeBtn = document.createElement('button');
    completeBtn.className = 'small-btn';
    completeBtn.type = 'button';
    completeBtn.textContent = 'Concluir';
    completeBtn.addEventListener('click', e => { e.stopPropagation(); completeGoal(goal.id); });
    actions.append(cancelBtn, completeBtn);
    head.append(title, actions);

    const box = document.createElement('div');
    box.className = 'goal-progress';
    const info = document.createElement('div');
    info.className = 'goal-progress-info';
    const percent = document.createElement('span');
    percent.className = 'goal-progress-percent';
    percent.textContent = `${Math.round(pct)}%`;
    const unitsLabel = document.createElement('span');
    unitsLabel.textContent = goalUnitsText(goal, progress.units || 0);
    info.append(percent, unitsLabel);
    const track = document.createElement('div');
    track.className = 'goal-progress-track';
    const bar = document.createElement('div');
    bar.className = 'goal-progress-bar';
    bar.style.width = `${pct}%`;
    track.appendChild(bar);
    box.append(info, track);

    card.append(head, box);
    ((goal.milestones) || []).forEach(m => card.appendChild(buildMilestoneRow(m)));
    return card;
}

function renderActivesList(actives) {
    if (!goalActivesList) return;
    goalActivesList.innerHTML = '';
    (actives || []).forEach(progress => goalActivesList.appendChild(buildGoalCard(progress)));
}

function updateGoalCard(card, progress) {
    const goal = progress.goal;
    const pct = Math.max(0, Math.min(100, progress.percent || 0));
    const percent = card.querySelector('.goal-progress-percent');
    if (percent) percent.textContent = `${Math.round(pct)}%`;
    const unitsLabel = card.querySelector('.goal-progress-info span:last-child');
    if (unitsLabel) unitsLabel.textContent = goalUnitsText(goal, progress.units || 0);
    const bar = card.querySelector('.goal-progress-bar');
    if (bar) bar.style.width = `${pct}%`;
    card.querySelectorAll('.goal-milestone').forEach(el => el.remove());
    ((goal.milestones) || []).forEach(m => card.appendChild(buildMilestoneRow(m)));
}

function findGoalCard(goalId) {
    if (!goalActivesList || !goalId) return null;
    return goalActivesList.querySelector(`.goal-active-card[data-goal-id="${goalId}"]`);
}

function fillGoalForm(goal) {
    currentGoalId = goal ? goal.id : 0;
    if (goalTitleInput) goalTitleInput.value = goal ? goal.title : '';
    if (goalGiftSelect) setSelectValue(goalGiftSelect, goal && goal.giftName ? goal.giftName : '');
    if (goalTargetInput) goalTargetInput.value = goal && goal.targetUnits ? String(goal.targetUnits) : '';
    const milestones = (goal && goal.milestones) || [];
    goalMilestoneRows.forEach((row, i) => {
        const m = milestones[i];
        const unitsInput = row.querySelector('.goal-ms-units');
        const rewardInput = row.querySelector('.goal-ms-reward');
        if (unitsInput) unitsInput.value = m && m.atUnits ? String(m.atUnits) : '';
        if (rewardInput) setSelectValue(rewardInput, m ? (m.reward || '') : '');
    });
    updateGoalButtons();
}

function resetGoalForm() {
    currentGoalId = 0;
    if (goalTitleInput) goalTitleInput.value = '';
    if (goalGiftSelect) goalGiftSelect.value = '';
    if (goalTargetInput) goalTargetInput.value = '';
    goalMilestoneRows.forEach(row => {
        const unitsInput = row.querySelector('.goal-ms-units');
        const rewardInput = row.querySelector('.goal-ms-reward');
        if (unitsInput) unitsInput.value = '';
        if (rewardInput) setSelectValue(rewardInput, '');
    });
    updateGoalButtons();
}

function resetGoalDisplay() {
    resetGoalForm();
    renderActivesList([]);
    renderGoalHistory([]);
}

function collectMilestones() {
    return goalMilestoneRows.map(row => {
        const rawUnits = parseInt(row.querySelector('.goal-ms-units').value, 10);
        const reward = row.querySelector('.goal-ms-reward').value.trim();
        return {
            atUnits: Number.isNaN(rawUnits) ? 0 : rawUnits,
            reward
        };
    }).filter(m => m.atUnits > 0 || m.reward !== '');
}

function renderGoalHistory(history) {
    if (!goalHistoryList || !goalHistoryWrap) return;
    goalHistoryList.innerHTML = '';
    if (!history || history.length === 0) {
        goalHistoryWrap.hidden = true;
        return;
    }
    goalHistoryWrap.hidden = false;
    history.forEach(goal => {
        const item = document.createElement('div');
        item.className = 'goal-history-item';
        const title = document.createElement('span');
        title.className = 'goal-history-title';
        const giftName = goal.giftName || '';
        title.textContent = giftName
            ? `${goal.title} — ${goal.targetUnits} ${translateGiftName(giftName)}`
            : `${goal.title} — ${goal.targetUnits} unidades`;
        const tag = document.createElement('span');
        tag.className = `goal-status-tag ${goal.status}`;
        tag.textContent = goal.status === 'completed' ? 'Concluída'
            : goal.status === 'cancelled' ? 'Cancelada'
            : goal.status;
        item.append(title, tag);
        goalHistoryList.appendChild(item);
    });
}

async function loadGoals() {
    try {
        const response = await fetch('/api/goals');
        if (!response.ok) {
            console.error('[Frontend] loadGoals: status', response.status);
            return;
        }
        const state = await response.json();
        renderActivesList(state.actives || []);
        renderGoalHistory(state.history || []);
        if (currentGoalId > 0 && !(state.actives || []).some(p => p.goal.id === currentGoalId)) {
            resetGoalForm(); // a meta em edição foi concluída/cancelada
        }
    } catch (error) {
        console.error('[Frontend] loadGoals: erro:', error);
    }
}

async function saveGoal() {
    const title = goalTitleInput ? goalTitleInput.value.trim() : '';
    const giftName = goalGiftSelect ? goalGiftSelect.value.trim() : '';
    const targetUnits = goalTargetInput ? parseInt(goalTargetInput.value, 10) : NaN;
    if (!title) {
        showGoalToast('Dê um título à meta.');
        return;
    }
    if (!targetUnits || targetUnits < 1) {
        showGoalToast('Defina a meta em unidades (mínimo 1).');
        return;
    }
    try {
        const res = await fetch('/api/goals', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                ...(currentGoalId ? { id: currentGoalId } : {}),
                title,
                giftName,
                targetUnits,
                milestones: collectMilestones()
            })
        });
        if (!res.ok) {
            let message = `status ${res.status}`;
            try {
                const payload = await res.json();
                if (payload.error) message = payload.error;
            } catch (e) { /* mantém mensagem padrão */ }
            showGoalToast(`Erro ao salvar meta: ${message}`);
            return;
        }
        const saved = await res.json();
        const wasEdit = currentGoalId > 0;
        currentGoalId = saved.id;
        await loadGoals();
        if (!wasEdit) resetGoalForm(); // formulário pronto para a próxima meta
        showGoalToast(wasEdit ? 'Meta atualizada.' : 'Meta criada.');
    } catch (error) {
        console.error('[Frontend] saveGoal: erro:', error);
        showGoalToast('Erro ao salvar meta.');
    }
}

async function cancelGoal(goalId) {
    if (!goalId) return;
    if (!confirm('Cancelar a meta?')) return;
    try {
        const res = await fetch(`/api/goals/cancel?id=${goalId}`, { method: 'POST' });
        if (!res.ok) {
            showGoalToast('Erro ao cancelar a meta.');
            return;
        }
        if (currentGoalId === goalId) resetGoalForm();
        await loadGoals();
        showGoalToast('Meta cancelada.');
    } catch (error) {
        console.error('[Frontend] cancelGoal: erro:', error);
        showGoalToast('Erro ao cancelar a meta.');
    }
}

async function completeGoal(goalId) {
    if (!goalId) return;
    if (!confirm('Concluir a meta agora?')) return;
    try {
        const res = await fetch(`/api/goals/complete?id=${goalId}`, { method: 'POST' });
        if (!res.ok) {
            showGoalToast('Erro ao concluir a meta.');
            return;
        }
        if (currentGoalId === goalId) resetGoalForm();
        await loadGoals();
        showGoalToast('🎉 Meta concluída!');
    } catch (error) {
        console.error('[Frontend] completeGoal: erro:', error);
        showGoalToast('Erro ao concluir a meta.');
    }
}

goalSaveBtn.addEventListener('click', saveGoal);
goalResetBtn.addEventListener('click', resetGoalForm);
updateGoalButtons();

// --- Administração: lives e horários ---

function formatAdminTime(value) {
    if (!value) return '—';
    const date = new Date(value);
    if (isNaN(date.getTime())) return value;
    return date.toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' });
}

function formatAdminDuration(startedAt, endedAt) {
    const start = new Date(startedAt).getTime();
    const end = new Date(endedAt).getTime();
    if (isNaN(start) || isNaN(end) || end < start) return '—';
    const minutes = Math.round((end - start) / 60000);
    const hours = Math.floor(minutes / 60);
    const rest = minutes % 60;
    if (hours === 0) return `${rest}min`;
    return `${hours}h ${rest}min`;
}

function renderAdminLives(lives) {
    if (!adminLivesTableBody) return;
    adminLivesTableBody.innerHTML = '';
    if (!lives || lives.length === 0) {
        const tr = document.createElement('tr');
        tr.innerHTML = '<td colspan="7" style="text-align:center; opacity:0.6;">Nenhuma live registrada</td>';
        adminLivesTableBody.appendChild(tr);
        return;
    }
    lives.forEach(live => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td data-label="Live">${escapeHtml(live.name || '—')}</td>
            <td data-label="Dia">${escapeHtml(live.day || '—')}</td>
            <td data-label="Início">${formatAdminTime(live.startedAt)}</td>
            <td data-label="Fim">${formatAdminTime(live.endedAt)}</td>
            <td data-label="Duração">${formatAdminDuration(live.startedAt, live.endedAt)}</td>
            <td data-label="Eventos">${live.events ?? 0}</td>
            <td data-label="Ações"><button class="small-btn" type="button" style="border-color: var(--pink);">Deletar</button></td>
        `;
        tr.querySelector('button').addEventListener('click', () => deleteAdminLive(live.name));
        adminLivesTableBody.appendChild(tr);
    });
}

async function loadAdminLives() {
    if (!adminLivesTableBody) return;
    try {
        const response = await fetch(`/api/admin/lives?limit=${adminLivesLimit}`);
        if (!response.ok) {
            throw new Error(`status ${response.status}`);
        }
        const data = await response.json();
        const lives = data.lives || [];
        renderAdminLives(lives);
        if (adminLivesMoreBtn) {
            adminLivesMoreBtn.style.display = lives.length >= adminLivesLimit ? 'inline-block' : 'none';
        }
    } catch (error) {
        console.error('[Frontend] Falha ao carregar lives da administração:', error);
        if (adminLivesTableBody) {
            adminLivesTableBody.innerHTML = '';
            const tr = document.createElement('tr');
            tr.innerHTML = '<td colspan="7" style="text-align:center; opacity:0.6;">Não foi possível carregar as lives</td>';
            adminLivesTableBody.appendChild(tr);
        }
    }
}

if (adminLivesRefreshBtn) {
    adminLivesRefreshBtn.addEventListener('click', () => {
        adminLivesLimit = 100;
        loadAdminLives();
    });
}

function openDeleteLiveModal(liveName) {
    deleteLivePending = liveName;
    deleteLiveModalMessage.textContent = `Deletar TODOS os dados da live "${liveName}" do banco? Essa ação não pode ser desfeita.`;
    deleteLiveModalBackdrop.classList.add('is-open');
    deleteLiveModalBackdrop.setAttribute('aria-hidden', 'false');
}

function closeDeleteLiveModal() {
    deleteLivePending = null;
    deleteLiveModalBackdrop.classList.remove('is-open');
    deleteLiveModalBackdrop.setAttribute('aria-hidden', 'true');
}

function deleteAdminLive(liveName) {
    if (!liveName || liveName === '—') return;
    openDeleteLiveModal(liveName);
}

if (deleteLiveModalConfirmBtn) {
    deleteLiveModalConfirmBtn.addEventListener('click', async () => {
        const liveName = deleteLivePending;
        if (!liveName) return;
        closeDeleteLiveModal();
        try {
            const response = await fetch('/api/admin/lives/delete?live=' + encodeURIComponent(liveName), { method: 'POST' });
            if (!response.ok) {
                throw new Error(`status ${response.status}`);
            }
            loadAdminLives();
        } catch (error) {
            console.error('[Frontend] Falha ao deletar live:', error);
            alert('Não foi possível deletar a live.');
        }
    });
}

if (deleteLiveModalCancelBtn) {
    deleteLiveModalCancelBtn.addEventListener('click', closeDeleteLiveModal);
}

if (deleteLiveModalCloseBtn) {
    deleteLiveModalCloseBtn.addEventListener('click', closeDeleteLiveModal);
}

if (deleteLiveModalBackdrop) {
    deleteLiveModalBackdrop.addEventListener('click', event => {
        if (event.target === deleteLiveModalBackdrop) {
            closeDeleteLiveModal();
        }
    });
}

if (adminLivesMoreBtn) {
    adminLivesMoreBtn.addEventListener('click', () => {
        adminLivesLimit += 100;
        loadAdminLives();
    });
}

async function bootstrap() {
    renderTargetGifts();

    try {
        await ensureBrowserChart();
        const ChartLib = window.Chart;
        if (!ChartLib) {
            throw new Error('Chart.js indisponível.');
        }
        chart = createChart(ChartLib);
    } catch (e) {
        console.error('Chart.js init error:', e);
        setStatus(`Erro ao iniciar gráfico: ${e.message}`, 'error');
        // Don't return; let the rest of bootstrap run.
    }

    await loadInitialState();
    setupEventStream();
    loadAdminLives();
}

void bootstrap();

// ============================================================
// Selects pesquisáveis: transforma todos os <select> da página
// em combobox (campo de texto + lista filtrável) para o usuário
// poder digitar e achar o que procura.
// O <select> original permanece no DOM (escondido), então todo o
// código existente que lê/escreve select.value continua valendo.
// ============================================================
const searchableSelectWidgets = [];

function initSearchableSelects() {
    document.querySelectorAll('select').forEach(select => {
        if (select.dataset.ssWrapped) return;
        select.dataset.ssWrapped = '1';

        // Wrapper + input visível no lugar do select. As classes do select
        // (setup-select / small) são copiadas para reaproveitar o estilo.
        const wrap = document.createElement('div');
        wrap.className = 'ss-wrap';
        select.parentNode.insertBefore(wrap, select);
        select.style.display = 'none';
        wrap.appendChild(select);

        const input = document.createElement('input');
        input.type = 'text';
        input.className = `${select.className} ss-input`.trim();
        input.autocomplete = 'off';
        input.spellcheck = false;

        const menu = document.createElement('div');
        menu.className = 'ss-menu';
        menu.hidden = true;

        wrap.appendChild(input);
        wrap.appendChild(menu);

        const state = { open: false, active: -1 };
        const widget = {
            select,
            input,
            lastValue: select.value,
            sync() { if (document.activeElement !== input) syncInput(); }
        };

        const visibleRows = () => Array.from(menu.querySelectorAll('.ss-option'));

        function currentOption() {
            for (const o of select.options) {
                if (o.value === select.value) return o;
            }
            return null;
        }

        // Mantém o texto do input em sincronia com o valor atual do select.
        function syncInput() {
            const chosen = currentOption();
            if (chosen && select.value !== '') {
                input.value = chosen.textContent.trim();
            } else {
                input.value = '';
                input.placeholder = chosen ? chosen.textContent.trim() : '';
            }
        }

        // Reconstrói a lista, filtrando pelo texto digitado (busca tanto no
        // rótulo quanto no value, ex.: "soccer" acha "Futebol (Soccer)").
        function rebuildMenu() {
            menu.innerHTML = '';
            const query = input.value.trim().toLowerCase();
            let count = 0;
            for (const o of select.options) {
                if (o.disabled) continue;
                const label = o.textContent.trim();
                if (query && !label.toLowerCase().includes(query) &&
                    !o.value.toLowerCase().includes(query)) continue;
                const row = document.createElement('div');
                row.className = 'ss-option' + (o.value === select.value ? ' ss-selected' : '');
                row.textContent = label;
                row.dataset.value = o.value;
                row.addEventListener('mousedown', e => {
                    e.preventDefault(); // evita o blur antes da seleção
                    choose(o.value);
                });
                menu.appendChild(row);
                count++;
            }
            if (count === 0) {
                const empty = document.createElement('div');
                empty.className = 'ss-no-results';
                empty.textContent = 'Nenhum resultado';
                menu.appendChild(empty);
            }
            const rows = visibleRows();
            let idx = rows.findIndex(r => r.classList.contains('ss-selected'));
            setActive(query || idx < 0 ? 0 : idx);
        }

        function setActive(i) {
            const rows = visibleRows();
            state.active = rows.length ? Math.min(Math.max(i, 0), rows.length - 1) : -1;
            rows.forEach((r, idx) => r.classList.toggle('ss-active', idx === state.active));
            const activeRow = rows[state.active];
            if (activeRow) activeRow.scrollIntoView({ block: 'nearest' });
        }

        function openMenu() {
            state.open = true;
            menu.hidden = false;
            // Se o texto é exatamente o rótulo selecionado (abertura por
            // clique/foco), limpa para mostrar todas as opções de novo.
            const chosen = currentOption();
            if (chosen && input.value.trim() === chosen.textContent.trim()) {
                input.value = '';
            }
            rebuildMenu();
            input.focus();
        }

        function closeMenu() {
            state.open = false;
            menu.hidden = true;
            syncInput(); // devolve o texto ao valor selecionado
        }

        function choose(value) {
            select.value = value;
            widget.lastValue = value;
            syncInput();
            state.open = false;
            menu.hidden = true;
        }

        input.addEventListener('focus', openMenu);
        input.addEventListener('click', () => { if (!state.open) openMenu(); });
        input.addEventListener('input', () => {
            if (!state.open) { state.open = true; menu.hidden = false; }
            rebuildMenu();
        });
        input.addEventListener('blur', closeMenu);
        input.addEventListener('keydown', e => {
            if (e.key === 'ArrowDown') {
                e.preventDefault();
                if (!state.open) openMenu(); else setActive(state.active + 1);
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                if (!state.open) openMenu(); else setActive(state.active - 1);
            } else if (e.key === 'Enter') {
                e.preventDefault();
                const rows = visibleRows();
                const row = rows[state.active] || rows[0];
                if (row) choose(row.dataset.value);
            } else if (e.key === 'Escape') {
                e.stopPropagation();
                closeMenu();
            } else if (e.key === 'Tab') {
                closeMenu();
            }
        });

        // As opções mudam por fora (innerHTML reescrito etc.) → reflete na lista.
        new MutationObserver(() => {
            if (state.open) rebuildMenu();
        }).observe(select, { childList: true });

        searchableSelectWidgets.push(widget);
        syncInput();
    });

    // O código do programa muda select.value sem disparar evento
    // (ex.: setSelectValue) → polling leve para manter o campo em sync.
    setInterval(() => {
        for (const w of searchableSelectWidgets) {
            if (w.select.value !== w.lastValue) {
                w.lastValue = w.select.value;
                w.sync();
            }
        }
    }, 400);
}

initSearchableSelects();
