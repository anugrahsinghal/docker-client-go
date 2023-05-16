//go:build linux

package main

import (
	"archive/tar"
	"compress/gzip"
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
	"time"
)

func handleErr(msg string, err error) {
	if err != nil {
		fmt.Printf(msg+" - Err: %v", err)
		os.Exit(1)
	}
}

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]
	dockerImage := os.Args[2]
	imageName, imageTag := getImageInfo(dockerImage)

	dockerClient := DokcerClient{
		httpClient: &http.Client{},
		imageName:  imageName,
		imageTag:   imageTag,
	}

	token, err := dockerClient.AuthToken()
	handleErr("Auth", err)

	dockerClient.token = token

	dokcerManifest, err := dockerClient.ManifestFile()

	handleErr("Docker Manifest", err)
	fmt.Printf("Manifest %v\n", dokcerManifest)

	// 1. create a temp dir
	tempDirPath, err := ioutil.TempDir("/tmp", "ccf-")
	handleErr("tempdir", err)

	// 2. copy binary being executed
	err = copyCommandExecutableToTempDir(tempDirPath, command)
	handleErr("copyCommandExecutableToTempDir", err)
	err = createDevNull(tempDirPath)
	handleErr("createDevNull", err)

	fmt.Printf("Args: %v\n", os.Args)

	// 3. chroot to temp dir
	if err := syscall.Chroot(tempDirPath); err != nil {
		fmt.Printf("Chroot - Err: %v", err)
		os.Exit(1)
	}

	//{
	//	[
	//		{{sha256:ca5534a51dd04bbcebe9b23ba05f389466cf0c190f1f8f182d7eea92a9671d00 application/vnd.oci.image.manifest.v1+json 424} {amd64 linux }}
	//		{{sha256:2faed463fb00a57a51cc1fe0e0884d46eacac8e7784ca7a93c3e861661d3e752 application/vnd.oci.image.manifest.v1+json 424} {arm linux v7}}
	//		{{sha256:6f8fe7bff0bee25c481cdc26e28bba984ebf72e6152005c18e1036983c01a28b application/vnd.oci.image.manifest.v1+json 424} {arm64 linux v8}}
	//		{{sha256:93fbac516e3f64e076e953306215d0a05e691e8350bf7c2e6b600ed2678990e5 application/vnd.oci.image.manifest.v1+json 424} {ppc64le linux }}
	//		{{sha256:6f86459a9bb50cb27768ad53ba78d6f02612bbf7f1efeb513569bd2160c76834 application/vnd.oci.image.manifest.v1+json 424} {s390x linux }}
	//	]
	//	application/vnd.oci.image.index.v1+json
	//	2
	//}

	folder := dockerClient.imageName
	os.MkdirAll(folder, os.ModeDir)

	for _, manifest := range dokcerManifest.Manifests {
		if dokcerManifest.SchemaVersion == 2 {
			digestManifest, err := dockerClient.DigestManifestFile(manifest)
			handleErr("DigestManifestFile", err)
			fmt.Printf("digestManifest - %v\n", digestManifest)
			for _, layer := range digestManifest.Layers {
				err := dockerClient.PullLayer(folder, layer)
				handleErr("Pull V2 layer", err)
			}
		}
	}

	dirs, err := ioutil.ReadDir(folder)
	handleErr("", err)
	fmt.Printf("tmpdir = %v\n", tempDirPath)
	for _, dir := range dirs {
		fmt.Printf("dir = %v\n", dir.Name())
	}
	time.Sleep(1 * time.Second)
	cmd := exec.Command(command, args...)
	//cmd.Stdin = os.Stdin // to ensure no err on cmd.Run()

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}

	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			// access fields of exitErr here
			os.Exit(exitErr.ExitCode())
		} else {
			fmt.Printf("Err: %v", exitErr)
			os.Exit(1)
		}
	}

	//output, err := cmd.Output()
	//if err != nil {
	//	fmt.Printf("Err: %v", err)
	//	os.Exit(1)
	//}
	//
	//fmt.Println(string(output))

}

func getImageInfo(dockerImage string) (string, string) {
	image := strings.Split(dockerImage, ":")
	imageName := "library/" + image[0]
	imageTag := image[1]
	fmt.Printf("Docker %v", image)
	return imageName, imageTag
}

func copyCommandExecutableToTempDir(tempDirPath string, command string) error {
	sourceFile := command
	input, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		fmt.Printf("read - Err: %v", err)
		return err
	}

	destinationFile := path.Join(tempDirPath, sourceFile)
	err = os.MkdirAll(tempDirPath+"/usr/local/bin", os.ModeDir)
	if err != nil {
		fmt.Printf("MkdirAll - Err: %v", err)
		return err
	}
	err = ioutil.WriteFile(destinationFile, input, os.ModePerm)
	if err != nil {
		fmt.Printf("write - Err: %v", err)
		return err
	}

	return nil
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

	//auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	req, err := http.NewRequest("GET", "https://auth.docker.io/token?service=registry.docker.io&scope=repository:"+dockerClient.imageName+":pull", nil)
	if err != nil {
		return "", err
	}
	//req.Header.Add("Authorization", "Basic "+auth)
	resp, err := dockerClient.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", err
	}
	token := result["token"].(string)
	//fmt.Printf("token %v", token)

	return token, err
}

func (dockerClient DokcerClient) ManifestFile() (DokcerManifest, error) {
	req, err := http.NewRequest("GET", "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/manifests/"+dockerClient.imageTag, nil)
	if err != nil {
		return DokcerManifest{}, err
	}
	req.Header.Add("Authorization", "Bearer "+dockerClient.token)
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")

	resp, err := dockerClient.httpClient.Do(req)
	if err != nil {
		return DokcerManifest{}, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return DokcerManifest{}, err
	}
	var result DokcerManifest
	err = json.Unmarshal(body, &result)
	if err != nil {
		return DokcerManifest{}, err
	}

	return result, nil
}

func (dockerClient DokcerClient) DigestManifestFile(manifest Manifest) (V2DigestManifest, error) {
	req, err := http.NewRequest("GET", "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/manifests/"+manifest.Digest, nil)
	if err != nil {
		return V2DigestManifest{}, err
	}
	req.Header.Add("Authorization", "Bearer "+dockerClient.token)
	req.Header.Add("Accept", manifest.MediaType)

	resp, err := dockerClient.httpClient.Do(req)
	if err != nil {
		return V2DigestManifest{}, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return V2DigestManifest{}, err
	}
	var result V2DigestManifest
	err = json.Unmarshal(body, &result)
	if err != nil {
		return V2DigestManifest{}, err
	}

	return result, nil
}

func (dockerClient DokcerClient) PullLayer(folder string, layer Layer) error {
	//GET /v2/<name>/blobs/<digest>
	fmt.Printf("Pulling layer :%v\n", layer.Digest)
	req, err := http.NewRequest("GET", "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/blobs/"+layer.Digest, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+dockerClient.token)
	req.Header.Add("Accept", layer.MediaType)

	resp, err := dockerClient.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create a gzip reader
	gzipReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		panic(err)
	}
	defer gzipReader.Close()

	// Create a tar reader
	tarReader := tar.NewReader(gzipReader)

	// Extract files from the archive
	for {
		_, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}

		//fmt.Println(header.Name)

		// Write the file to disk

		file, err := os.Create(path.Join(folder, layer.Digest))
		if err != nil {
			panic(err)
		}
		defer file.Close()

		_, err = io.Copy(file, tarReader)
		if err != nil {
			panic(err)
		}
	}
	return nil

}

type DokcerManifest struct {
	Manifests     []Manifest `json:"manifests"`
	MediaType     string     `json:"mediaType"`
	SchemaVersion int        `json:"schemaVersion"`
}

type Manifest struct {
	Layer
	Platform struct {
		Architecture string `json:"architecture"`
		Os           string `json:"os"`
		Variant      string `json:"variant,omitempty"`
	} `json:"platform"`
}

type Config struct {
	Layer
}

// type: application/vnd.oci.image.index.v1+json
/*
{
"manifests":[
	{"digest":"sha256:ca5534a51dd04bbcebe9b23ba05f389466cf0c190f1f8f182d7eea92a9671d00","mediaType":"application\/vnd.oci.image.manifest.v1+json","platform":{"architecture":"amd64","os":"linux"},"size":424},
	{"digest":"sha256:2faed463fb00a57a51cc1fe0e0884d46eacac8e7784ca7a93c3e861661d3e752","mediaType":"application\/vnd.oci.image.manifest.v1+json", "platform":{"architecture":"arm","os":"linux","variant":"v7"},"size":424},
	{"digest":"sha256:6f8fe7bff0bee25c481cdc26e28bba984ebf72e6152005c18e1036983c01a28b","mediaType":"application\/vnd.oci.image.manifest.v1+json", "platform":{"architecture":"arm64","os":"linux","variant":"v8"},"size":424},
	{"digest":"sha256:93fbac516e3f64e076e953306215d0a05e691e8350bf7c2e6b600ed2678990e5","mediaType":"application\/vnd.oci.image.manifest.v1+json","platform":{"architecture":"ppc64le","os":"linux"},"size":424},
	{"digest":"sha256:6f86459a9bb50cb27768ad53ba78d6f02612bbf7f1efeb513569bd2160c76834","mediaType":"application\/vnd.oci.image.manifest.v1+json",
		"platform":{
			"architecture":"s390x",
			"os":"linux"
		},
		"size":424
	}
],
"mediaType":"application\/vnd.oci.image.index.v1+json",
"schemaVersion":2
}
*/
type V2DigestManifest struct {
	Config        Config  `json:"config"`
	Layers        []Layer `json:"layers"`
	MediaType     string  `json:"mediaType"`
	SchemaVersion int     `json:"schemaVersion"`
}
type Layer struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
}

//map[
//config:
//	map[
//		digest:sha256:aa44e363572ab7e275bd5517e384c3e0e9e0f0d69e926aa68e85eaafc35e1373
//		mediaType:application/vnd.oci.image.config.v1+json
//		size:2299
//	]
//layers:
//	[
//		map[
//			digest:sha256:27be50396e3447f2e65a022e1d493eb54352cd28760269836b6a06f99639e4cf
//			mediaType:application/vnd.oci.image.layer.v1.tar+gzip
//			size:2.8017013e+07
//		]
//	]
//mediaType:application/vnd.oci.image.manifest.v1+json
//schemaVersion:2
//]
