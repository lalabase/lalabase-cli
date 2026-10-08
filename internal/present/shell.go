package present

// Shell contains display values, never credentials or wire records.
type Shell struct {
	Organisation Inline
	List         Body
	Object       Body
}
