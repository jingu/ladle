// Package completion provides shell completion script generation.
package completion

import (
	"fmt"
	"io"
)

// Shell represents a supported shell type.
type Shell string

const (
	ShellBash Shell = "bash"
	ShellZsh  Shell = "zsh"
	ShellFish Shell = "fish"
)

// Generate writes a shell completion script to the given writer.
func Generate(w io.Writer, shell Shell) error {
	switch shell {
	case ShellBash:
		return generateBash(w)
	case ShellZsh:
		return generateZsh(w)
	case ShellFish:
		return generateFish(w)
	default:
		return fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish)", shell)
	}
}

func generateBash(w io.Writer) error {
	script := `# ladle bash completion
_ladle_completions() {
    local cur prev opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    opts="--meta --editor --profile --region --endpoint-url --no-sign-request --yes --force --dry-run --help --version"

    # Complete S3 URIs
    if [[ "$cur" == s3://* ]]; then
        local bucket_and_path="${cur#s3://}"
        local bucket="${bucket_and_path%%/*}"

        if [[ "$bucket_and_path" == */* ]]; then
            # Complete keys within bucket
            local prefix="${bucket_and_path#*/}"
            local profile_arg=""
            for ((i=0; i<${#COMP_WORDS[@]}; i++)); do
                if [[ "${COMP_WORDS[$i]}" == "--profile" ]]; then
                    profile_arg="--profile ${COMP_WORDS[$((i+1))]}"
                fi
            done
            local keys
            keys=$(ladle --complete-path $profile_arg "s3://${bucket}/${prefix}" 2>/dev/null)
            while IFS= read -r key; do
                COMPREPLY+=("$key")
            done <<< "$keys"
        else
            # Complete bucket names
            local profile_arg=""
            for ((i=0; i<${#COMP_WORDS[@]}; i++)); do
                if [[ "${COMP_WORDS[$i]}" == "--profile" ]]; then
                    profile_arg="--profile ${COMP_WORDS[$((i+1))]}"
                fi
            done
            local buckets
            buckets=$(ladle --complete-bucket $profile_arg "$bucket" 2>/dev/null)
            while IFS= read -r b; do
                COMPREPLY+=("s3://${b}/")
            done <<< "$buckets"
        fi
        return 0
    fi

    case "${prev}" in
        --profile|--region|--endpoint-url|--editor)
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "${opts}" -- "${cur}") )
        return 0
    fi
}
complete -o nospace -F _ladle_completions ladle
`
	_, err := fmt.Fprint(w, script)
	return err
}

func generateZsh(w io.Writer) error {
	script := `#compdef ladle

_ladle() {
    local -a opts
    opts=(
        '--meta[Edit metadata instead of file content]'
        '--editor[Specify editor command]:editor:'
        '--profile[AWS named profile]:profile:'
        '--region[AWS region]:region:'
        '--endpoint-url[Custom endpoint URL]:url:'
        '--no-sign-request[Do not sign requests]'
        '--yes[Skip confirmation prompt]'
        '--force[Force operation on binary files]'
        '--dry-run[Show diff without uploading]'
        '--help[Show help]'
        '--version[Show version]'
    )

    _arguments -s $opts '*:uri:_ladle_uri'
}

_ladle_uri() {
    local cur="${words[CURRENT]}"

    if [[ "$cur" == s3://* ]]; then
        local bucket_and_path="${cur#s3://}"
        local bucket="${bucket_and_path%%/*}"

        if [[ "$bucket_and_path" == */* ]]; then
            local prefix="${bucket_and_path#*/}"
            local profile_arg=""
            local -i i
            for ((i=1; i<$#words; i++)); do
                if [[ "${words[$i]}" == "--profile" ]]; then
                    profile_arg="--profile ${words[$((i+1))]}"
                fi
            done
            local -a keys
            keys=(${(f)"$(ladle --complete-path $profile_arg "s3://${bucket}/${prefix}" 2>/dev/null)"})
            compadd -S '' -q -- $keys
        else
            local profile_arg=""
            local -i i
            for ((i=1; i<$#words; i++)); do
                if [[ "${words[$i]}" == "--profile" ]]; then
                    profile_arg="--profile ${words[$((i+1))]}"
                fi
            done
            local -a buckets
            buckets=(${(f)"$(ladle --complete-bucket $profile_arg "$bucket" 2>/dev/null)"})
            compadd -S '/' -q -- ${buckets[@]/#/s3://}
        fi
    else
        compadd -S '://' -- s3 gs az r2
    fi
}

compdef _ladle ladle
`
	_, err := fmt.Fprint(w, script)
	return err
}

func generateFish(w io.Writer) error {
	script := `# ladle fish completion
complete -c ladle -l meta -d 'Edit metadata instead of file content'
complete -c ladle -l editor -r -d 'Specify editor command'
complete -c ladle -l profile -r -d 'AWS named profile'
complete -c ladle -l region -r -d 'AWS region'
complete -c ladle -l endpoint-url -r -d 'Custom endpoint URL'
complete -c ladle -l no-sign-request -d 'Do not sign requests'
complete -c ladle -s y -l yes -d 'Skip confirmation prompt'
complete -c ladle -l force -d 'Force operation on binary files'
complete -c ladle -l dry-run -d 'Show diff without uploading'
complete -c ladle -l help -d 'Show help'
complete -c ladle -l version -d 'Show version'

function __ladle_complete_uri
    set -l cur (commandline -ct)
    if string match -q 's3://*' -- $cur
        set -l bucket_and_path (string replace 's3://' '' -- $cur)
        if string match -q '*/*' -- $bucket_and_path
            set -l profile_arg
            set -l tokens (commandline -po)
            for i in (seq (count $tokens))
                if test "$tokens[$i]" = "--profile"
                    set profile_arg --profile $tokens[(math $i + 1)]
                end
            end
            ladle --complete-path $profile_arg $cur 2>/dev/null
        else
            set -l profile_arg
            set -l tokens (commandline -po)
            for i in (seq (count $tokens))
                if test "$tokens[$i]" = "--profile"
                    set profile_arg --profile $tokens[(math $i + 1)]
                end
            end
            for b in (ladle --complete-bucket $profile_arg $bucket_and_path 2>/dev/null)
                echo "s3://$b/"
            end
        end
    else
        printf '%s\n' 's3://' 'gs://' 'az://' 'r2://'
    end
end

complete -c ladle -a '(__ladle_complete_uri)' -f
`
	_, err := fmt.Fprint(w, script)
	return err
}
