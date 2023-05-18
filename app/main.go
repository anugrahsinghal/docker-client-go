//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
)

const ImageIndexMediatype = "application/vnd.oci.image.index.v1+json"

func handleErr(msg string, err error) {
	if err != nil {
		fmt.Printf(msg+" - Err: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	dockerImage := os.Args[2]
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	imageName, imageTag := getImageInfo(dockerImage)

	dockerClient := DokcerClient{
		httpClient: &http.Client{},
		imageName:  imageName,
		imageTag:   imageTag,
	}

	token, err := dockerClient.AuthToken()
	handleErr("Auth", err)

	dockerClient.token = token

	imageIndexFile, err := dockerClient.ImageIndexFile()

	handleErr("Docker Manifest", err)

	tempDirPath, err := ioutil.TempDir("", "")
	handleErr("tempdir", err)

	if imageIndexFile.SchemaVersion == 2 {
		v2digestManifest, err := dockerClient.DigestManifestFile(
			imageIndexFile.Manifests[0].Digest, imageIndexFile.Manifests[0].MediaType,
		)
		handleErr("DigestManifestFile", err)

		for _, layer := range v2digestManifest.Layers {
			err := dockerClient.PullAndExtractLayer(tempDirPath, layer.Digest, layer.MediaType)
			handleErr("Pull V2 layer", err)
		}
	} else if imageIndexFile.SchemaVersion == 1 {
		for _, layer := range imageIndexFile.FSLayers {
			err := dockerClient.PullAndExtractLayer(tempDirPath, layer.BlobSum, "")
			handleErr("Pull V2 layer", err)
		}
	}

	err = createDevNull(tempDirPath)
	handleErr("createDevNull", err)

	if err := syscall.Chroot(tempDirPath); err != nil {
		os.Exit(1)
	}

	cmd := exec.Command(command, args...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}

	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			os.Exit(exitErr.ExitCode())
		} else {
			os.Exit(1)
		}
	}

}

func getImageInfo(dockerImage string) (string, string) {
	image := strings.Split(dockerImage, ":")
	imageName := "library/" + image[0]

	var imageTag string
	if len(image) != 2 {
		imageTag = "latest"
	} else {
		imageTag = image[1]
	}
	return imageName, imageTag
}

func createDevNull(tempDirPath string) error {
	devNullPath := path.Join(tempDirPath, "dev")
	if err := os.MkdirAll(devNullPath, os.ModeDir); err != nil {
		return err
	}

	return ioutil.WriteFile(path.Join(tempDirPath, "dev", "null"), []byte{}, os.ModePerm)
}

type DokcerClient struct {
	httpClient *http.Client
	token      string
	imageName  string
	imageTag   string
}

func (dockerClient DokcerClient) AuthToken() (string, error) {
	var result map[string]interface{}
	body := doGet(dockerClient, "https://auth.docker.io/token?service=registry.docker.io&scope=repository:"+dockerClient.imageName+":pull", "")

	err := json.Unmarshal(body, &result)
	if err != nil {
		return "", err
	}
	token := result["token"].(string)

	return token, err
}

func (dockerClient DokcerClient) ImageIndexFile() (ImageIndex, error) {
	var result ImageIndex
	body := doGet(dockerClient, "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/manifests/"+dockerClient.imageTag, ImageIndexMediatype)

	err := json.Unmarshal(body, &result)
	if err != nil {
		return ImageIndex{}, err
	}

	return result, nil
}

func (dockerClient DokcerClient) DigestManifestFile(digest, mediaType string) (V2DigestManifest, error) {
	var result V2DigestManifest
	body := doGet(dockerClient, "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/manifests/"+digest, mediaType)

	err := json.Unmarshal(body, &result)
	if err != nil {
		return V2DigestManifest{}, err
	}

	return result, nil
}

func (dockerClient DokcerClient) PullAndExtractLayer(tempDirPath, layerDigest, mediaType string) error {
	//GET /v2/<name>/blobs/<digest>
	req, err := http.NewRequest("GET", "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/blobs/"+layerDigest, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+dockerClient.token)
	req.Header.Add("Accept", mediaType)

	resp, err := dockerClient.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	fileName := strings.Split(layerDigest, ":")[1] + ".tar.gz"

	file, err := os.Create(fileName)

	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	cmd := exec.Command("tar", "-xzf", fileName, "-C", tempDirPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil

}

func doGet(dockerClient DokcerClient, uri string, mediaType string) []byte {
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return []byte{}
	}
	if dockerClient.token != "" {
		req.Header.Add("Authorization", "Bearer "+dockerClient.token)
		req.Header.Add("Accept", mediaType)
	}

	resp, err := dockerClient.httpClient.Do(req)
	if err != nil {
		return []byte{}
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		handleErr("Reading Response Body", err)
	}

	return body
}

type ImageIndex struct {
	Manifests     []Manifest `json:"manifests"` // v2
	FSLayers      []FSLayer  `json:"fsLayers"`  // v1
	SchemaVersion int        `json:"schemaVersion"`
}
type FSLayer struct {
	BlobSum string `json:"blobSum"`
}
type Manifest struct {
	Layer
}
type Config struct {
	Layer
}
type V2DigestManifest struct {
	Config        Config  `json:"config"`
	Layers        []Layer `json:"layers"`
	SchemaVersion int     `json:"schemaVersion"`
}
type Layer struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
}
