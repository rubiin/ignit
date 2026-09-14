package main

import (
	"fmt"
	"github.com/gernest/wow"
	"github.com/gernest/wow/spin"
	"github.com/manifoldco/promptui"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

func getEnvironments() []string {
	loadingSpinner := wow.New(os.Stdout, spin.Get(spin.Dots), " Getting environments")
	loadingSpinner.Start()
	resp, err := http.Get("https://www.gitignore.io/api/list")
	if err != nil {
		log.Fatalln(err)
	}

	defer resp.Body.Close()

	//We Read the response body on the line below
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalln(err)
	}
	//Convert the body to type string
	sb := string(body)
	items := strings.Split(sb, ",")
	loadingSpinner.Stop()
	return items
}

func writeFile(language string) {
	addingSpinner := wow.New(os.Stdout, spin.Get(spin.Dots), "Adding gitignore")

	giResponse, err := http.Get(fmt.Sprintf("https://www.gitignore.io/api/%s", language))
	if err != nil {
		log.Fatalln(err)
	}
	defer giResponse.Body.Close()

	//We Read the response body on the line below.
	giBody, err := io.ReadAll(giResponse.Body)
	if err != nil {
		log.Fatalln(err)
	}
	//Convert the body to type string
	addingSpinner.Stop()
	err = os.WriteFile(".gitignore", giBody, 0644)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Successfully created gitignore for %s\n", language)
}

func main() {
	items := getEnvironments()
	prompt := promptui.Select{
		Label:             "Select environment",
		Items:             items,
		StartInSearchMode: true,
		Searcher: func(input string, index int) bool {
			pepper := items[index]
			name := strings.Replace(strings.ToLower(pepper), " ", "", -1)
			input = strings.Replace(strings.ToLower(input), " ", "", -1)

			return strings.Contains(name, input)
		},
	}

	_, result, err := prompt.Run()

	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}
	writeFile(result)
}
