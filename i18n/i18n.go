package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// DefaultLang is the fallback language
const DefaultLang = "ru"

// SupportedLangs lists all supported languages
var SupportedLangs = []string{"ru", "kk", "en"}

// Translator handles message localization
type Translator struct {
	messages map[string]map[string]string // lang -> key -> message
	mu       sync.RWMutex
}

var (
	defaultTranslator *Translator
	once              sync.Once
)

// Default returns the default translator instance
func Default() *Translator {
	once.Do(func() {
		defaultTranslator = New()
		// Load embedded translations
		defaultTranslator.LoadDefaults()
	})
	return defaultTranslator
}

// New creates a new Translator
func New() *Translator {
	return &Translator{
		messages: make(map[string]map[string]string),
	}
}

// LoadDefaults loads default error messages
func (t *Translator) LoadDefaults() {
	// Russian (default)
	t.SetMessages("ru", map[string]string{
		// Common errors
		"internal_error":     "Внутренняя ошибка сервера",
		"not_found":          "Не найдено",
		"unauthorized":       "Необходима авторизация",
		"forbidden":          "Доступ запрещен",
		"bad_request":        "Неверный запрос",
		"validation_error":   "Ошибка валидации",
		"rate_limit_exceeded": "Превышен лимит запросов",

		// Auth errors
		"invalid_phone":      "Неверный формат номера телефона",
		"invalid_code":       "Неверный код подтверждения",
		"code_expired":       "Код подтверждения истек",
		"code_too_soon":      "Подождите перед повторным запросом кода",
		"token_expired":      "Токен истек",
		"invalid_token":      "Неверный токен",

		// User errors
		"user_not_found":     "Пользователь не найден",
		"user_already_exists": "Пользователь уже существует",
		"profile_incomplete": "Профиль не заполнен",

		// Garage errors
		"car_not_found":      "Автомобиль не найден",
		"invalid_year":       "Неверный год выпуска",
		"invalid_mileage":    "Неверный пробег",
		"invalid_vin":        "Неверный VIN код",
		"invalid_license_plate": "Неверный госномер",
		"invalid_document_type": "Неверный тип документа",
		"invalid_service_type":  "Неверный тип услуги",
		"invalid_reminder_type": "Неверный тип напоминания",
		"mark_required":      "Выберите марку автомобиля",
		"model_required":     "Выберите модель автомобиля",
		"year_required":      "Укажите год выпуска",
		"document_not_found": "Документ не найден",
		"reminder_not_found": "Напоминание не найдено",
		"service_record_not_found": "Запись о сервисе не найдена",
		"photo_not_found":          "Фото не найдено",

		// NSI errors
		"mark_id_required":  "Укажите ID марки",
		"model_id_required": "Укажите ID модели",
		"platform_required": "Укажите платформу (ios, android)",

		// Organization errors
		"organization_not_found": "Организация не найдена",
		"already_member":     "Вы уже являетесь участником",
		"invalid_invite_code": "Неверный код приглашения",

		// Request types
		"request_type.repair":  "Ремонт",
		"request_type.parts":   "Запчасти",
		"request_type.unknown": "Неизвестный тип",

		// Request statuses
		"request_status.moderation": "На модерации",
		"request_status.published":  "Активная",
		"request_status.completed":  "Завершена",
		"request_status.unknown":    "Неизвестный статус",
	})

	// Kazakh
	t.SetMessages("kk", map[string]string{
		// Common errors
		"internal_error":     "Сервердің ішкі қатесі",
		"not_found":          "Табылмады",
		"unauthorized":       "Авторизация қажет",
		"forbidden":          "Кіруге тыйым салынған",
		"bad_request":        "Қате сұраныс",
		"validation_error":   "Валидация қатесі",
		"rate_limit_exceeded": "Сұраныс шегі асып кетті",

		// Auth errors
		"invalid_phone":      "Телефон нөмірінің форматы қате",
		"invalid_code":       "Растау коды қате",
		"code_expired":       "Растау кодының мерзімі өтті",
		"code_too_soon":      "Кодты қайта сұрау алдында күтіңіз",
		"token_expired":      "Токеннің мерзімі өтті",
		"invalid_token":      "Қате токен",

		// User errors
		"user_not_found":     "Пайдаланушы табылмады",
		"user_already_exists": "Пайдаланушы бар",
		"profile_incomplete": "Профиль толтырылмаған",

		// Garage errors
		"car_not_found":      "Автокөлік табылмады",
		"invalid_year":       "Шығарылған жылы қате",
		"invalid_mileage":    "Жүгіріс қате",
		"invalid_vin":        "VIN коды қате",
		"invalid_license_plate": "Мемлекеттік нөмір қате",
		"mark_required":      "Автокөлік маркасын таңдаңыз",
		"model_required":     "Автокөлік моделін таңдаңыз",
		"year_required":      "Шығарылған жылын көрсетіңіз",
		"photo_not_found":    "Фото табылмады",
		"mark_id_required":   "Марка ID көрсетіңіз",
		"model_id_required":  "Модель ID көрсетіңіз",
		"platform_required":  "Платформаны көрсетіңіз (ios, android)",

		// Request types
		"request_type.repair":  "Жөндеу",
		"request_type.parts":   "Қосалқы бөлшектер",
		"request_type.unknown": "Белгісіз тип",

		// Request statuses
		"request_status.moderation": "Модерацияда",
		"request_status.published":  "Белсенді",
		"request_status.completed":  "Аяқталған",
		"request_status.unknown":    "Белгісіз мәртебе",
	})

	// English
	t.SetMessages("en", map[string]string{
		// Common errors
		"internal_error":     "Internal server error",
		"not_found":          "Not found",
		"unauthorized":       "Authorization required",
		"forbidden":          "Access denied",
		"bad_request":        "Bad request",
		"validation_error":   "Validation error",
		"rate_limit_exceeded": "Rate limit exceeded",

		// Auth errors
		"invalid_phone":      "Invalid phone number format",
		"invalid_code":       "Invalid verification code",
		"code_expired":       "Verification code expired",
		"code_too_soon":      "Please wait before requesting a new code",
		"token_expired":      "Token expired",
		"invalid_token":      "Invalid token",

		// User errors
		"user_not_found":     "User not found",
		"user_already_exists": "User already exists",
		"profile_incomplete": "Profile incomplete",

		// Garage errors
		"car_not_found":      "Car not found",
		"invalid_year":       "Invalid year",
		"invalid_mileage":    "Invalid mileage",
		"invalid_vin":        "Invalid VIN code",
		"invalid_license_plate": "Invalid license plate",
		"invalid_document_type": "Invalid document type",
		"invalid_service_type":  "Invalid service type",
		"invalid_reminder_type": "Invalid reminder type",
		"mark_required":      "Please select a car mark",
		"model_required":     "Please select a car model",
		"year_required":      "Please specify the year",
		"document_not_found": "Document not found",
		"reminder_not_found": "Reminder not found",
		"service_record_not_found": "Service record not found",
		"photo_not_found":          "Photo not found",

		// NSI errors
		"mark_id_required":  "Mark ID is required",
		"model_id_required": "Model ID is required",
		"platform_required": "Platform is required (ios, android)",

		// Organization errors
		"organization_not_found": "Organization not found",
		"already_member":     "You are already a member",
		"invalid_invite_code": "Invalid invite code",

		// Request types
		"request_type.repair":  "Repair",
		"request_type.parts":   "Parts",
		"request_type.unknown": "Unknown type",

		// Request statuses
		"request_status.moderation": "Under review",
		"request_status.published":  "Active",
		"request_status.completed":  "Completed",
		"request_status.unknown":    "Unknown status",
	})
}

// SetMessages sets messages for a language
func (t *Translator) SetMessages(lang string, messages map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.messages[lang] == nil {
		t.messages[lang] = make(map[string]string)
	}
	for k, v := range messages {
		t.messages[lang][k] = v
	}
}

// LoadFromJSON loads messages from JSON bytes
func (t *Translator) LoadFromJSON(lang string, data []byte) error {
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		return fmt.Errorf("unmarshal messages: %w", err)
	}
	t.SetMessages(lang, messages)
	return nil
}

// LoadFromFS loads messages from embedded filesystem
func (t *Translator) LoadFromFS(fs embed.FS, pattern string) error {
	files, err := fs.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		lang := strings.TrimSuffix(name, ".json")
		data, err := fs.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read file %s: %w", name, err)
		}

		if err := t.LoadFromJSON(lang, data); err != nil {
			return fmt.Errorf("load %s: %w", lang, err)
		}
	}

	return nil
}

// T translates a message key to the specified language
func (t *Translator) T(lang, key string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Normalize language code (e.g., "ru-RU" -> "ru")
	lang = normalizeLang(lang)

	// Try requested language
	if msgs, ok := t.messages[lang]; ok {
		if msg, ok := msgs[key]; ok {
			return msg
		}
	}

	// Fallback to default language
	if lang != DefaultLang {
		if msgs, ok := t.messages[DefaultLang]; ok {
			if msg, ok := msgs[key]; ok {
				return msg
			}
		}
	}

	// Return key if no translation found
	return key
}

// TF translates with format arguments
func (t *Translator) TF(lang, key string, args ...any) string {
	msg := t.T(lang, key)
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// T is a convenience function using the default translator
func T(lang, key string) string {
	return Default().T(lang, key)
}

// TF is a convenience function using the default translator with formatting
func TF(lang, key string, args ...any) string {
	return Default().TF(lang, key, args...)
}

// normalizeLang extracts base language from locale (e.g., "ru-RU" -> "ru")
func normalizeLang(lang string) string {
	if lang == "" {
		return DefaultLang
	}

	// Handle "ru-RU", "ru_RU" formats
	lang = strings.ToLower(lang)
	if idx := strings.IndexAny(lang, "-_"); idx > 0 {
		lang = lang[:idx]
	}

	// Check if supported
	for _, supported := range SupportedLangs {
		if lang == supported {
			return lang
		}
	}

	return DefaultLang
}

// ParseAcceptLanguage parses Accept-Language header and returns the best match
func ParseAcceptLanguage(header string) string {
	if header == "" {
		return DefaultLang
	}

	// Simple parsing - get first language
	// Full implementation would handle quality values (q=0.9)
	parts := strings.Split(header, ",")
	for _, part := range parts {
		lang := strings.TrimSpace(strings.Split(part, ";")[0])
		normalized := normalizeLang(lang)
		if normalized != DefaultLang || lang == DefaultLang {
			return normalized
		}
	}

	return DefaultLang
}
