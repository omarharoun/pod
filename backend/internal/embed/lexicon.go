package embed

// The concept lexicon: what this embedder knows instead of a hosted model.
//
// Matching hard-filters on goal category *before* it scores anything (CLAUDE.md §6), so
// this signal is only ever asked to compare two goals that are already in the same
// category. That is what the lexicon is built for. It does not need to know that running
// is unlike a tax return; it needs to know that running is like jogging and unlike yoga,
// inside "movement". Concepts are therefore sub-topics of a category, not categories.
//
// Coverage is English and it is finite. Anything the lexicon has never heard of falls
// through to the lexical features, which are what the whole embedder used to be — so an
// unknown vocabulary is no worse off than it was before, and a known one is much better
// off. That asymmetry is the point: this is a floor being raised, not a bet being placed.
//
// Adding a concept is a backfill (`cmd/reembed`), because stored vectors are laid out by
// concept index. Nothing else about it is delicate: entries are plain words, readable by
// somebody who wants to check whether their goal is covered.

// conceptWords maps a category to its concepts, and a concept to the surface forms that
// mean it. Forms are stemmed at init by the same `stem` the input goes through, so
// "jogging" here and "jogged" in somebody's goal land on the same entry.
//
// Nested by category because the category is known when a goal is embedded, and looking
// up only that category's concepts is what stops "clear" in "clear my overdraft" from
// spending half its weight on decluttering, or "boxes" in a home goal on boxing.
var conceptWords = map[string]map[string][]string{
	"movement": {
		"running":  {"run", "running", "jog", "jogging", "sprint", "marathon", "5k", "10k", "parkrun", "runner"},
		"walking":  {"walk", "walking", "steps", "stroll", "hike", "hiking", "rambling", "ramble"},
		"cycling":  {"cycle", "cycling", "bike", "biking", "bicycle", "spin", "peloton"},
		"swimming": {"swim", "swimming", "pool", "lengths", "lane", "lido"},
		"gym":      {"gym", "weights", "lifting", "lift", "barbell", "dumbbell", "squat", "deadlift", "bench", "reps", "workout"},
		"yoga":     {"yoga", "pilates", "stretch", "stretching", "mobility", "flexibility", "flexible"},
		"climbing": {"climb", "climbing", "bouldering", "boulder"},
		"sport":    {"football", "soccer", "basketball", "tennis", "badminton", "netball", "cricket", "hockey", "squash", "golf", "rowing"},
		"dance":    {"dance", "dancing", "zumba", "ballet", "salsa"},
		"martial":  {"boxing", "box", "karate", "judo", "jiu", "taekwondo", "martial", "kickboxing"},
	},
	"creative": {
		"writing":     {"write", "writing", "novel", "story", "stories", "poem", "poetry", "blog", "screenplay", "manuscript", "chapter", "draft", "writer"},
		"music":       {"music", "guitar", "piano", "sing", "singing", "song", "songs", "band", "drums", "violin", "ukulele", "keyboard", "compose"},
		"drawing":     {"draw", "drawing", "sketch", "sketching", "illustration", "illustrate", "doodle", "comic"},
		"painting":    {"paint", "painting", "watercolour", "watercolor", "acrylic", "oils", "canvas"},
		"photography": {"photo", "photos", "photography", "camera", "photograph", "shoot", "darkroom", "film"},
		"craft":       {"knit", "knitting", "crochet", "sew", "sewing", "quilt", "pottery", "ceramics", "woodwork", "carpentry", "craft", "embroidery"},
		"filmmaking":  {"video", "edit", "editing", "youtube", "podcast", "record", "recording", "documentary"},
		"code":        {"code", "coding", "program", "programming", "app", "website", "software", "game", "side project"},
	},
	"admin": {
		"paperwork":    {"paperwork", "forms", "form", "admin", "filing", "documents", "document", "scan", "shred", "post"},
		"tax":          {"tax", "taxes", "hmrc", "receipts", "expenses", "accounts", "bookkeeping", "invoice", "invoices"},
		"email":        {"email", "emails", "inbox", "unsubscribe", "mailbox", "messages"},
		"appointments": {"appointment", "appointments", "booking", "dentist", "optician", "schedule", "reschedule"},
		"renewals":     {"insurance", "renew", "renewal", "licence", "license", "passport", "visa", "registration", "mot", "expired"},
		"cancelling":   {"cancel", "subscription", "subscriptions", "membership", "memberships", "unused"},
	},
	"learning": {
		"language": {"language", "spanish", "french", "german", "italian", "japanese", "mandarin", "chinese", "portuguese", "duolingo", "vocabulary", "fluent", "grammar"},
		"course":   {"course", "class", "classes", "module", "lecture", "lectures", "mooc", "certificate", "syllabus", "coursera", "curriculum"},
		"reading":  {"read", "reading", "book", "books", "pages", "kindle", "library", "reader"},
		"study":    {"study", "studying", "revise", "revision", "exam", "exams", "notes", "flashcards", "memorise", "memorize", "practise"},
		"research": {"research", "paper", "papers", "thesis", "dissertation", "journal article", "literature"},
	},
	"career": {
		"jobsearch":  {"job", "jobs", "application", "applications", "apply", "cv", "resume", "vacancy", "recruiter", "interview", "interviews", "hiring"},
		"portfolio":  {"portfolio", "showcase", "case study", "samples"},
		"networking": {"network", "networking", "linkedin", "mentor", "mentoring", "conference", "meetup", "introduce"},
		"promotion":  {"promotion", "raise", "appraisal", "manager", "feedback", "review", "progression", "title"},
		"freelance":  {"business", "freelance", "freelancing", "client", "clients", "startup", "launch", "customers", "pitch"},
		"upskilling": {"certification", "certified", "qualification", "training", "upskill", "accreditation"},
	},
	"home": {
		"declutter": {"declutter", "clutter", "tidy", "tidying", "sort", "sorting", "clear", "clear out", "clearing", "organise", "organize", "junk", "stuff", "boxes", "hoard", "rid", "get rid", "chuck", "bin", "charity shop", "jumble"},
		"cleaning":  {"clean", "cleaning", "hoover", "vacuum", "dust", "dusting", "mop", "scrub", "wash", "washing", "laundry"},
		"diy":       {"diy", "fix", "fixing", "repair", "shelf", "shelves", "decorate", "decorating", "assemble", "flatpack", "drill", "tile", "plaster"},
		"garden":    {"garden", "gardening", "plants", "weeds", "weeding", "lawn", "mow", "allotment", "seeds", "planting", "greenhouse", "compost"},
		"rooms":     {"room", "kitchen", "bedroom", "bathroom", "garage", "loft", "attic", "shed", "cupboard", "wardrobe", "drawer", "spare", "hall", "hallway", "basement", "cellar", "porch"},
		"moving":    {"move", "moving", "pack", "packing", "unpack", "removals", "storage"},
	},
	"health": {
		"sleep":    {"sleep", "sleeping", "bed", "bedtime", "insomnia", "rest", "wake", "waking", "alarm", "nap", "tired"},
		"food":     {"eat", "eating", "food", "cook", "cooking", "meal", "meals", "diet", "vegetables", "veg", "sugar", "snack", "snacking", "breakfast", "lunch", "dinner", "nutrition", "takeaway", "portion"},
		"drinking": {"drink", "drinking", "alcohol", "booze", "beer", "wine", "sober", "dry", "pub"},
		"smoking":  {"smoke", "smoking", "cigarette", "cigarettes", "vape", "vaping", "nicotine", "quit"},
		"screen":   {"screen", "phone", "scrolling", "scroll", "instagram", "tiktok", "twitter", "doomscrolling", "notifications"},
		"mind":     {"meditate", "meditation", "mindfulness", "breathe", "breathing", "anxiety", "therapy", "therapist", "journal", "journalling", "gratitude", "stress", "counselling"},
		"medical":  {"doctor", "gp", "checkup", "physio", "medication", "bloods", "screening", "smear", "referral"},
		"water":    {"water", "hydrate", "hydration", "hydrated"},
	},
	"connection": {
		"family":    {"mum", "mom", "dad", "mother", "father", "parents", "sister", "brother", "sibling", "gran", "grandma", "grandad", "grandmother", "grandfather", "aunt", "uncle", "cousin", "family", "nephew", "niece"},
		"friends":   {"friend", "friends", "mate", "mates", "godson", "goddaughter", "neighbour", "neighbor"},
		"calling":   {"call", "calling", "ring", "facetime", "voicemail", "voice note"},
		"writingto": {"message", "text", "letter", "letters", "card", "postcard", "birthday", "reply", "replying", "whatsapp"},
		"reconnect": {"reconnect", "touch", "apologise", "apologize", "sorry", "estranged", "fallen out", "amends"},
		"meeting":   {"visit", "meet", "coffee", "drinks", "see", "seeing", "together"},
	},
	"money": {
		"budget":  {"budget", "budgeting", "spending", "spend", "track", "tracking", "outgoings", "spreadsheet"},
		"saving":  {"save", "saving", "savings", "isa", "emergency fund", "deposit", "pot", "aside"},
		"debt":    {"debt", "debts", "loan", "loans", "overdraft", "credit", "credit card", "repay", "repayment", "owe", "owed", "borrowed", "arrears", "clear", "paying off", "settle"},
		"pension": {"pension", "retirement", "invest", "investing", "investment", "investments", "stocks", "shares", "index"},
		"bills":   {"bills", "bill", "energy", "switch", "tariff", "mortgage", "rent", "council tax", "utilities", "broadband"},
	},
}

// settingConcepts name a place, an occasion or a means rather than a thing somebody is
// doing.
//
// They are real evidence — two people both working on the loft have more in common than
// one on the loft and one on the garden — but they are weaker evidence than the activity,
// and scoring them as peers gets it wrong in a specific way. "Sort out the spare room"
// and "repair the bathroom door" are both about rooms and are not the same goal;
// "visit my sister" and "meet a friend for coffee" are both a meeting, and the thing that
// actually makes two of these goals alike is *who*, not whether it happens by phone.
//
// A setting narrows a topic. It is not one.
var settingConcepts = map[string]bool{
	"rooms":     true,
	"calling":   true,
	"writingto": true,
	"meeting":   true,
}

// conceptParents groups concepts that are genuinely alike *within* a category. A parent
// is what makes "start swimming" and "get back on the bike" score above "start swimming"
// and "sort the loft" without making them score as though they were the same goal.
//
// There is deliberately no parent for the category itself. Every candidate in a
// comparison already shares one, so it would add the same constant to every pair and
// discriminate nothing while compressing the range that does.
var conceptParents = map[string][]string{
	"running": {"cardio"}, "walking": {"cardio", "gentle"}, "cycling": {"cardio"},
	"swimming": {"cardio"}, "gym": {"strength"}, "climbing": {"strength"},
	"yoga": {"gentle"}, "sport": {"team"}, "dance": {"team"}, "martial": {"team", "strength"},

	"drawing": {"visual"}, "painting": {"visual"}, "photography": {"visual"},
	"craft": {"making"}, "code": {"making"}, "filmmaking": {"making"},
	"writing": {"composing"}, "music": {"composing"},

	"tax": {"obligation"}, "renewals": {"obligation"}, "appointments": {"obligation"},
	"email": {"backlog"}, "paperwork": {"backlog"}, "cancelling": {"backlog"},

	"reading": {"intake"}, "course": {"intake"}, "research": {"intake"},
	"language": {"drilling"}, "study": {"drilling"},

	"jobsearch": {"seeking"}, "portfolio": {"seeking"}, "networking": {"seeking"},
	"promotion": {"growing"}, "upskilling": {"growing"}, "freelance": {"growing"},

	"declutter": {"tidying"}, "cleaning": {"tidying"}, "moving": {"tidying"},
	"diy": {"improving"}, "garden": {"improving"}, "rooms": {"improving"},

	"drinking": {"substance"}, "smoking": {"substance"}, "screen": {"substance"},
	"food": {"body"}, "water": {"body"}, "medical": {"body"},
	"sleep": {"restoring"}, "mind": {"restoring"},

	"calling": {"reaching"}, "writingto": {"reaching"}, "meeting": {"reaching"},
	"family": {"who"}, "friends": {"who"}, "reconnect": {"who"},

	"budget": {"outflow"}, "debt": {"outflow"}, "bills": {"outflow"},
	"saving": {"building"}, "pension": {"building"},
}
