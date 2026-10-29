package domain

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
}

var DemoUsers = []User{
	{ID: "u-alice", Name: "Alice", Note: "saved card with autopay consent, email notifications"},
	{ID: "u-bob", Name: "Bob", Note: "saved card WITHOUT autopay consent, SMS notifications"},
	{ID: "u-carol", Name: "Carol", Note: "autopay card, email + SMS + push notifications"},
}

func FindUser(id string) (User, bool) {
	for _, user := range DemoUsers {
		if user.ID == id {
			return user, true
		}
	}
	return User{}, false
}
