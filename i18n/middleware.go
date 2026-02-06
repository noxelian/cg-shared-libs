package i18n

import (
	"context"
)

type contextKey string

const langContextKey contextKey = "lang"

// ContextWithLang adds language to context
func ContextWithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langContextKey, normalizeLang(lang))
}

// LangFromContext extracts language from context
func LangFromContext(ctx context.Context) string {
	if lang, ok := ctx.Value(langContextKey).(string); ok {
		return lang
	}
	return DefaultLang
}

// GinMiddleware returns a Gin middleware that extracts language from Accept-Language header
// Usage: router.Use(i18n.GinMiddleware())
//
// Example with gin:
//
//	func (h *Handler) GetUser(c *gin.Context) {
//	    lang := i18n.LangFromContext(c.Request.Context())
//	    // or directly from header
//	    lang := i18n.ParseAcceptLanguage(c.GetHeader("Accept-Language"))
//	    msg := i18n.T(lang, "user_not_found")
//	}
