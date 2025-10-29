package latitude_test

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/latitude"
)

func TestLatitudeClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Latitude Client Unit Test Suite")
}

var _ = Describe("Client", func() {
	Describe("NewClient", func() {
		It("should return error when LATITUDE_API_KEY is not set", func() {
			// Unset the env var if it exists
			oldKey := os.Getenv("LATITUDE_API_KEY")
			os.Unsetenv("LATITUDE_API_KEY")
			defer func() {
				if oldKey != "" {
					os.Setenv("LATITUDE_API_KEY", oldKey)
				}
			}()

			_, err := latitude.NewClient()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("LATITUDE_API_KEY"))
		})

		It("should create client successfully with valid API key", func() {
			os.Setenv("LATITUDE_API_KEY", "test-key")
			defer os.Unsetenv("LATITUDE_API_KEY")

			client, err := latitude.NewClient()
			Expect(err).NotTo(HaveOccurred())
			Expect(client).NotTo(BeNil())
		})
	})
})
