/*
* @Author: mustafa
* @Date:   2016-03-29 17:31:09
* @Last Modified by:   mstg
* @Last Modified time: 2016-03-30 05:32:57
*/

package main

import (
  "os"
  "log"
  "debug/macho"
  "github.com/mstg/machotbd/modules"
  "errors"
  "strings"
  "encoding/binary"
  "bytes"
  "fmt"
  "sort"
  "flag"
)

const (
  arm64 macho.Cpu = 16777228

  // From <mach-o/nlist.h>
  N_TYPE uint8 = 0x0e
  N_SECT uint8 = 0xe
  N_EXT uint8 = 0x01
  LoadDylibIdCmd = 0xd
  fileHeaderSize32 = 7 * 4
  fileHeaderSize64 = 8 * 4
  ReExportDylibCmd = (0x1f | 0x80000000)
)

type DylibIdCmd_ struct {
  Cmd macho.LoadCmd
  Len uint32
  Name uint32
  Time uint32
  CurrentVersion uint32
  CompatVersion uint32
}

type ByLength []string

func (s ByLength) Len() int {
  return len(s)
}
func (s ByLength) Swap(i, j int) {
  s[i], s[j] = s[j], s[i]
}
func (s ByLength) Less(i, j int) bool {
  return len(s[i]) > len(s[j])
}


func cstring(b []byte) string {
  var i int
  for i = 0; i < len(b) && b[i] != 0; i++ {
  }
  return string(b[0:i])
}

func ver(raw_ver uint32) string {
  return fmt.Sprintf("%d.%d.%d", raw_ver >> 16, (raw_ver >> 8) & 0xff, raw_ver & 0xff)
}

func magic_type(magic uint32) (uint32) {
  if magic == macho.Magic32 {
    return 32
  } else if magic == macho.Magic64 {
    return 64
  } else if magic == macho.MagicFat {
    return 1
  }

  return 0
}

func cpu_type(f *macho.File) (string) {
  if f.Cpu == macho.Cpu386 {
    return "i386"
  } else if f.Cpu == macho.CpuAmd64 {
    return "x86_64"
  } else if f.Cpu == macho.CpuArm && f.SubCpu == 6 {
    return "armv6"
  } else if f.Cpu == macho.CpuArm && f.SubCpu == 9 {
    return "armv7"
  } else if f.Cpu == macho.CpuArm && f.SubCpu == 11 {
    return "armv7s"
  } else if f.Cpu == arm64 {
    return "arm64"
  }

  return "uns"
}

func parse_macho(f *macho.File, stdout *log.Logger, stderr *log.Logger) (tbd.Arch, []string, error) {
  mt := magic_type(f.Magic)
  cput := cpu_type(f)

  var _syms tbd.Arch

  if cput == "uns" {
    return _syms, []string{}, errors.New("Unsupported arch")
  }

  stdout.Println(mt, "bit", cput, "slice")

  symtab := f.Symtab
  real_symbols := []string{}
  real_classes := []string{}
  real_ivars := []string{}
  real_weak := []string{}
  for _, v := range symtab.Syms {
    if v.Type & N_TYPE == N_SECT && v.Type & N_EXT == N_EXT {
      if v.Name != "" {
        if strings.Contains(v.Name, "_OBJC_CLASS") {
          real_name := strings.Replace(v.Name, "_OBJC_CLASS_$", "", -1)
          real_classes = append(real_classes, real_name)
        } else if strings.Contains(v.Name, "_OBJC_IVAR") {
          real_name := strings.Replace(v.Name, "_OBJC_IVAR_$", "", -1)
          real_ivars = append(real_ivars, real_name)
        } else if strings.Contains(v.Name, "_OBJC_METACLASS") {
        } else {
          // Sort weak and strong symbols
          if v.Type & N_SECT == N_SECT {
            real_symbols = append(real_symbols, v.Name)
          }/* else if v.Type & N_SECT != N_SECT {
            real_weak = append(real_weak, v.Name)
          }*/ // Disable weak symbol finding until I find a better solution
        }
      }
    }
  }

  version := "275.0"
  compatibility_version := ""
  path := ""
  real_reexports := []string{}

  bo := f.ByteOrder
  offset := int64(fileHeaderSize32)
  if f.Magic == macho.Magic64 {
    offset = fileHeaderSize64
  }
  for _, v := range f.Loads {
    dat := v.Raw()
    cmd, siz := uint32(bo.Uint32(dat[0:4])), bo.Uint32(dat[4:8])
    var cmddat []byte
    cmddat, dat = dat[0:siz], dat[siz:]
    offset += int64(siz)

    switch cmd {
    case LoadDylibIdCmd:
      var hdr DylibIdCmd_
      b := bytes.NewReader(cmddat)
      if err := binary.Read(b, bo, &hdr); err != nil {
        break
      }
      path = cstring(cmddat[hdr.Name:])
      version = ver(hdr.CurrentVersion)
      compatibility_version = ver(hdr.CompatVersion)
      break
    case ReExportDylibCmd:
      var hdr DylibIdCmd_
      b := bytes.NewReader(cmddat)
      if err := binary.Read(b, bo, &hdr); err != nil {
        break
      }
      path = cstring(cmddat[hdr.Name:])
      real_reexports = append(real_reexports, path)
      break
    }
  }

  if len(real_reexports) > 0 {
    sort.Sort(ByLength(real_reexports))
  }

  sort.Strings(real_weak)
  sort.Strings(real_symbols)
  sort.Strings(real_classes)
  sort.Strings(real_ivars)


  _syms = tbd.Arch{Name: cput, Symbols: real_symbols, Classes: real_classes, Ivars: real_ivars, Weak: real_weak, ReExports: real_reexports}
  return _syms, []string{version, path, compatibility_version}, nil
}

func parse_fat(f *macho.FatFile, stdout *log.Logger, stderr *log.Logger) (tbd.Tbd_list) {
  stdout.Println("Universal Mach-O")

  _ret_sym := tbd.Tbd_list{}
  for _, v := range f.Arches {
    _ret_macho_sym, info, err := parse_macho(v.File, stdout, stderr)
    if err == nil {
      _ret_sym.Archs = append(_ret_sym.Archs, _ret_macho_sym)
      _ret_sym.Install_name = info[1]
      _ret_sym.Version = info[0]
      _ret_sym.CompVersion = info[2]
    }
  }

  return _ret_sym
}

var out = flag.String("out", "", "path to the file should be exported to")
var print = flag.Bool("print", true, "print tbd to stdout")
var plt = flag.String("platform", "ios", "platform to define in the output tbd")

func macho_tbd(args []string) {
  stderr := log.New(os.Stderr, "[?] ", 0)
  stdout := log.New(os.Stdout, "[+] ", 0)
  file := ""
  if len(args) > 0 {
    file = args[0]
  } else {
    stderr.Println("No Mach-O file provided")
    os.Exit(1)
  }

  if *out != "" {
    *print = false
  }

  if *plt != "ios" && *plt != "macosx" {
    stderr.Println("Unsupported platform, only ios and macosx is supported")
    os.Exit(1)
  }

  macho_file, err := macho.Open(file)
  var macho_fat_file *macho.FatFile
  universal := false

  if err != nil {
    macho_fat_file, err = macho.OpenFat(file)
  }

  if err != nil {
    stderr.Println("Malformed or invalid Mach-O provided, err:", err)
    os.Exit(1)
  }

  if macho_fat_file != nil {
    universal = true
  }

  var _list tbd.Tbd_list
  if universal {
    _list = parse_fat(macho_fat_file, stdout, stderr)

    stdout.Println("Arch count:", len(_list.Archs))
  } else {
    _unpreplist, info, err := parse_macho(macho_file, stdout, stderr)
    if err == nil {
      arch_arr := []tbd.Arch{_unpreplist}
      _list = tbd.Tbd_list{Archs: arch_arr}
      _list.Install_name = info[1]
      _list.Version = info[0]
      _list.CompVersion = info[2]
    }
  }

  _list.Platform = *plt

  _buf := tbd.Tbd_form(_list)

  printit := 0
  if *print == true && *out == "" {
    stdout.Println(_buf.String())
  } else if *out != "" {
    _, err := os.Stat(*out)

    if os.IsNotExist(err) {
      var file, err = os.Create(*out)
      if err != nil {
        printit = 1
      } else {
        defer file.Close()
      }
    }

    file, err := os.OpenFile(*out, os.O_RDWR, 0644)

    if err != nil {
      printit = 1
    } else {
      defer file.Close()
    }

    _, err = file.WriteString(_buf.String())

    if err != nil {
      printit = 1
    } else {
      err = file.Sync()
      if err != nil {
        printit = 1
      }
    }
  }

  if printit == 1 {
    stderr.Println("An error occured during I/O, printing to stdout")
    stdout.Println(_buf.String())
  } else if *out != "" {
    stdout.Println("Wrote to", *out)
  }
}

func main() {
  flag.Parse()
  macho_tbd(flag.Args())
}
