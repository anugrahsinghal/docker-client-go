//go:build linux

package main

/*/
hypothesis:

- mac needs VM/QEMU so that it can mock the LINUS FS and thus can execute linux commands seamlessly
- what docker maybe does is that 
uses linux system calls
to create a seprate enviroment
cgroups are helpful here: they help isolate/limit resource usage between 2 env

a container is basically a full blown linux FS mounted at a different folder and nothing else ?

//*/

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

func handleErr(msg string, err error) {
	if err != nil {
		fmt.Printf(msg+" - Err: %v\n", err)
		os.Exit(1)
	}
}

// mydocker run ubuntu:latest /usr/local/bin/docker-explorer echo hey
// mydocker run ubuntu:latest /bin/echo hey
// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	dockerImage := os.Args[2]
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]
	fmt.Println("command " + command)
	fmt.Printf("args %v \n", args)

	imageName, imageTag := getImageInfo(dockerImage)

	dockerClient := DokcerClient{
		httpClient: &http.Client{},
		imageName:  imageName,
		imageTag:   imageTag,
	}

	token, err := dockerClient.AuthToken()
	handleErr("Auth", err)

	dockerClient.token = token

	// index file
	imageIndexFile, err := dockerClient.ImageIndexFile()

	handleErr("Docker Manifest", err)
	fmt.Printf("Manifest %v\n", imageIndexFile)

	// 1. create a temp dir
	tempDirPath, err := ioutil.TempDir("", "")
	handleErr("tempdir", err)

	fmt.Printf("Args: %v\n", os.Args)

	// dirs, err := ioutil.ReadDir(tempDirPath)
	// handleErr("", err)
	// fmt.Printf("Before extract tmpdir = %v\n", tempDirPath)
	// for _, file := range dirs {
	// 	fmt.Printf("file = name = %v , isDir = %v\n", file.Name(), file.IsDir())
	// }

	// ubunut:latest
	if imageIndexFile.SchemaVersion == 2 {
		v2digestManifest, err := dockerClient.DigestManifestFile(
			imageIndexFile.Manifests[0].Digest, imageIndexFile.Manifests[0].MediaType,
		)
		handleErr("DigestManifestFile", err)

		// pull layer into given folder
		for _, layer := range v2digestManifest.Layers {
			err := dockerClient.PullAndExtractLayer(tempDirPath, layer.Digest, layer.MediaType)
			handleErr("Pull V2 layer", err)
		}
	} else if imageIndexFile.SchemaVersion == 1 {
		// pull layer into given folder
		for _, layer := range imageIndexFile.FSLayers {
			err := dockerClient.PullAndExtractLayer(tempDirPath, layer.BlobSum, "")
			handleErr("Pull V2 layer", err)
		}
	}

	// 2.create null file in dev folder for cmd to execute
	err = createDevNull(tempDirPath)
	handleErr("createDevNull", err)

	// dirs, err := ioutil.ReadDir(tempDirPath)
	// dirs, err = ioutil.ReadDir(tempDirPath)
	// handleErr("", err)
	// fmt.Printf("After Extraction tmpdir = %v\n", tempDirPath)
	// for _, file := range dirs {
	// 	fmt.Printf("file = name = %v , isDir = %v\n", file.Name(), file.IsDir())
	// }

	// time.Sleep(1 * time.Second)

	// dirs, err = ioutil.ReadDir(tempDirPath)
	// handleErr("read tempDirPath", err)
	// for _, file := range dirs {
	// 	fmt.Printf("After extract file = name = %v , isDir = %v\n", file.Name(), file.IsDir())
	// }

	// 3. chroot to temp dir
	if err := syscall.Chroot(tempDirPath); err != nil {
		fmt.Printf("Chroot - Err: %v\n", err)
		os.Exit(1)
	}

	// handleErr("chdir", syscall.Chdir("/"))

	cmd := exec.Command(command, args...)
	// cmd.Stdin = os.Stdin // to ensure no err on cmd.Run()

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID,
	}

	fmt.Println("Running commands")

	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			// access fields of exitErr here
			os.Exit(exitErr.ExitCode())
		} else {
			fmt.Printf("exitErr: %v\n", err)
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
	
	var imageTag string
	if(len(image) != 2) {
		imageTag = "latest"
	} else {
		imageTag = image[1]
	}
	fmt.Printf("Docker %v\n", image)
	return imageName, imageTag
}

func copyCommandExecutableToTempDir(tempDirPath string, command string) error {
	sourceFile := command
	input, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		fmt.Printf("read - Err: %v\n", err)
		return err
	}

	destinationFile := path.Join(tempDirPath, sourceFile)
	err = os.MkdirAll(tempDirPath+"/usr/local/bin", os.ModeDir)
	if err != nil {
		fmt.Printf("MkdirAll - Err: %v\n", err)
		return err
	}
	err = ioutil.WriteFile(destinationFile, input, os.ModePerm)
	if err != nil {
		fmt.Printf("write - Err: %v\n", err)
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

	var result map[string]interface{}
	body, err := doGet(dockerClient, "https://auth.docker.io/token?service=registry.docker.io&scope=repository:"+dockerClient.imageName+":pull", "")

	if err != nil {
		return "", err
	}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", err
	}
	token := result["token"].(string)
	//fmt.Printf("token %v", token)

	return token, err
}

func (dockerClient DokcerClient) ImageIndexFile() (ImageIndex, error) {
	var result ImageIndex
	body, err := doGet(dockerClient, "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/manifests/"+dockerClient.imageTag, "application/vnd.oci.image.index.v1+json")

	if err != nil {
		return ImageIndex{}, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return ImageIndex{}, err
	}

	fmt.Printf("image index file %v\n", string(body))

	return result, nil
}

func (dockerClient DokcerClient) DigestManifestFile(digest, mediaType string) (V2DigestManifest, error) {
	var result V2DigestManifest
	body, err := doGet(dockerClient, "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/manifests/"+digest, mediaType)

	if err != nil {
		return V2DigestManifest{}, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return V2DigestManifest{}, err
	}

	fmt.Printf("DigestManifestFile file %v\n", string(body))

	return result, nil
}

func (dockerClient DokcerClient) PullAndExtractLayer(tempDirPath, layerDigest, mediaType string) error {
	//GET /v2/<name>/blobs/<digest>
	fmt.Printf("Pulling layer :%v\n", layerDigest)
	req, err := http.NewRequest("GET", "https://registry.hub.docker.com/v2/"+dockerClient.imageName+"/blobs/"+layerDigest, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+dockerClient.token)
	req.Header.Add("Accept", mediaType)

	// download layer
	resp, err := dockerClient.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	hash := strings.Split(layerDigest, ":")[1]

	file, err := os.Create(hash + ".tar.gz")

	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	// extract file
	fmt.Printf("Extracting layer :%v\n", layerDigest)
	cmd := exec.Command("tar", "-xzf", (hash + ".tar.gz"), "-C", tempDirPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil

}

func doGet(dockerClient DokcerClient, uri string, mediaType string) ([]byte, error) {
	fmt.Printf("URL %v, mediaType = %v\n", uri, mediaType)
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return []byte{}, err
	}
	// when need to get token
	if dockerClient.token != "" {
		req.Header.Add("Authorization", "Bearer "+dockerClient.token)
		req.Header.Add("Accept", mediaType)
	}

	resp, err := dockerClient.httpClient.Do(req)
	fmt.Printf("StatusCode = %v\n", resp.Status)
	if err != nil {
		return []byte{}, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []byte{}, err
	}

	return body, nil
}

type ImageIndex struct {
	Manifests     []Manifest `json:"manifests"`
	FSLayers      []FSLayer  `json:"fsLayers"`
	MediaType     string     `json:"mediaType"`
	SchemaVersion int        `json:"schemaVersion"`
}

type FSLayer struct {
	BlobSum string `json:"blobSum"`
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

/*/
apline:linix image index file
{
	"schemaVersion": 1,
	"name": "library/alpine",
	"tag": "latest",
	"architecture": "amd64",
	"fsLayers": [
	   {
		  "blobSum": "sha256:a3ed95caeb02ffe68cdd9fd84406680ae93d633cb16422d00e8a7c22955b46d4"
	   },
	   {
		  "blobSum": "sha256:8a49fdb3b6a5ff2bd8ec6a86c05b2922a0f7454579ecc07637e94dfd1d0639b6"
	   }
	],
	"history": [
	   {
		  "v1Compatibility": "{\"architecture\":\"amd64\",\"config\":{\"Hostname\":\"\",\"Domainname\":\"\",\"User\":\"\",\"AttachStdin\":false,\"AttachStdout\":false,\"AttachStderr\":false,\"Tty\":false,\"OpenStdin\":false,\"StdinOnce\":false,\"Env\":[\"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\"],\"Cmd\":[\"/bin/sh\"],\"Image\":\"sha256:fa9de512065d701938f44d4776827d838440ed00f1f51b1fff5f97f7378acf08\",\"Volumes\":null,\"WorkingDir\":\"\",\"Entrypoint\":null,\"OnBuild\":null,\"Labels\":null},\"container\":\"0aa41c99f485b1dbe59101f2bb8e6922d9bf7cc1745f1c768f988b1bd724f11a\",\"container_config\":{\"Hostname\":\"0aa41c99f485\",\"Domainname\":\"\",\"User\":\"\",\"AttachStdin\":false,\"AttachStdout\":false,\"AttachStderr\":false,\"Tty\":false,\"OpenStdin\":false,\"StdinOnce\":false,\"Env\":[\"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\"],\"Cmd\":[\"/bin/sh\",\"-c\",\"#(nop) \",\"CMD [\\\"/bin/sh\\\"]\"],\"Image\":\"sha256:fa9de512065d701938f44d4776827d838440ed00f1f51b1fff5f97f7378acf08\",\"Volumes\":null,\"WorkingDir\":\"\",\"Entrypoint\":null,\"OnBuild\":null,\"Labels\":{}},\"created\":\"2023-05-09T23:11:10.132147526Z\",\"docker_version\":\"20.10.23\",\"id\":\"9b53e1d18b8ca6d05f261f41688f674879603cd2160c4e9ded4c7a7b93baa591\",\"os\":\"linux\",\"parent\":\"149414ad771db6217a7e94adc1a8a85f96ba1b8a7deed38f48f22ee1b82e459b\",\"throwaway\":true}"
	   },
	   {
		  "v1Compatibility": "{\"id\":\"149414ad771db6217a7e94adc1a8a85f96ba1b8a7deed38f48f22ee1b82e459b\",\"created\":\"2023-05-09T23:11:10.007217553Z\",\"container_config\":{\"Cmd\":[\"/bin/sh -c #(nop) ADD file:7625ddfd589fb824ee39f1b1eb387b98f3676420ff52f26eb9d975151e889667 in / \"]}}"
	   }
	],
	"signatures": [
	   {
		  "header": {
			 "jwk": {
				"crv": "P-256",
				"kid": "DA66:MSF3:PU24:76KK:UP6N:H7TR:CVDC:C55Z:GAMM:2GKK:FXTW:CYBL",
				"kty": "EC",
				"x": "XuooKKbEO6caj7IL4oeRNqBKmhpUQ-_eY4fHIBU9SMg",
				"y": "Fa1jgKNRKQovoI4_E4rF5HCSA8LfXetOdTwdl1Gi99E"
			 },
			 "alg": "ES256"
		  },
		  "signature": "WNZyvIWAr1fY90RpI4moBZbzCYbFykc8_XBbd83t8Kt_i1TzG7vn5xPr-q74H_AKNSmpJe-wlnxESiGt6hPVUw",
		  "protected": "eyJmb3JtYXRMZW5ndGgiOjIwOTcsImZvcm1hdFRhaWwiOiJDbjAiLCJ0aW1lIjoiMjAyMy0wNS0xOFQxMDo0MDoyNloifQ"
	   }
	]
 }
//*/

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

/*//
map[
	architecture:amd64
	fsLayers:[
		map[blobSum:sha256:a3ed95caeb02ffe68cdd9fd84406680ae93d633cb16422d00e8a7c22955b46d4]
		map[blobSum:sha256:8a49fdb3b6a5ff2bd8ec6a86c05b2922a0f7454579ecc07637e94dfd1d0639b6]
	]
	history:[
		map[
			v1Compatibility:{
				"architecture":"amd64",
				"config":{"Hostname":"","Domainname":"","User":"","AttachStdin":false,"AttachStdout":false,"AttachStderr":false,"Tty":false,"OpenStdin":false,"StdinOnce":false,"Env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"],"Cmd":["/bin/sh"],
				"Image":"sha256:fa9de512065d701938f44d4776827d838440ed00f1f51b1fff5f97f7378acf08","Volumes":null,"WorkingDir":"","Entrypoint":null,"OnBuild":null,"Labels":null},
				"container":"0aa41c99f485b1dbe59101f2bb8e6922d9bf7cc1745f1c768f988b1bd724f11a",
				"container_config":{"Hostname":"0aa41c99f485","Domainname":"","User":"","AttachStdin":false,"AttachStdout":false,"AttachStderr":false,"Tty":false,"OpenStdin":false,"StdinOnce":false,
				"Env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"],
				"Cmd":["/bin/sh","-c","#(nop) ","CMD [\"/bin/sh\"]"],
				"Image":"sha256:fa9de512065d701938f44d4776827d838440ed00f1f51b1fff5f97f7378acf08","Volumes":null,"WorkingDir":"","Entrypoint":null,"OnBuild":null,"Labels":{}},"created":"2023-05-09T23:11:10.132147526Z","docker_version":"20.10.23","id":"9b53e1d18b8ca6d05f261f41688f674879603cd2160c4e9ded4c7a7b93baa591","os":"linux","parent":"149414ad771db6217a7e94adc1a8a85f96ba1b8a7deed38f48f22ee1b82e459b","throwaway":true
			}
			]
			map[v1Compatibility:{"id":"149414ad771db6217a7e94adc1a8a85f96ba1b8a7deed38f48f22ee1b82e459b","created":"2023-05-09T23:11:10.007217553Z","container_config":{"Cmd":["/bin/sh -c #(nop) ADD file:7625ddfd589fb824ee39f1b1eb387b98f3676420ff52f26eb9d975151e889667 in / "
			]
		}
		}
			]
			]
			name:library/alpine
			schemaVersion:1
			signatures:[
				map[
					header:
						map[
							alg:ES256
							jwk:map[
								crv:P-256 kid:BR2Q:AY3C:FBW6:BKZX:2OP5:I36Z:GDVY:LN4Z:G46A:SWRR:E6WI:4WPZ
								kty:EC x:5_jwPOuG8WJAm500LT3L9jkac-EjOiyNoh8f0tLSp90
								y:nU_snnb7XAYRYmgZZypZRj78pIS19JnKxU0eqxH4Pjg
							]
			]
			protected:eyJmb3JtYXRMZW5ndGgiOjIwOTcsImZvcm1hdFRhaWwiOiJDbjAiLCJ0aW1lIjoiMjAyMy0wNS0xOFQwOTozMzoyNVoifQ
			signature:HZezbpU8ktl1aQIQY2FlYLv_0aDxu22goSUIDPwXyvFeYbgXaAh6KiqCuzVYx8-cn4K0IceE70d0oIdnZ89q-A]
	]

	tag:latest]
//*/
