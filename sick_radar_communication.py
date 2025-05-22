import socket
import time
import struct
import re
import math
import os  # Para limpar o terminal

class SICKRadar:
    def __init__(self, ip, port=2111):
        self.ip = ip
        self.port = port
        self.sock = None
        self.connected = False
        self.debug_mode = False  # Controla exibição de informações detalhadas
        
    def connect(self):
        """Estabelece conexão TCP com o radar"""
        try:
            self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            self.sock.settimeout(5)
            self.sock.connect((self.ip, self.port))
            self.connected = True
            print(f"Conectado ao radar em {self.ip}:{self.port}")
            return True
        except Exception as e:
            print(f"Erro ao conectar: {e}")
            self.connected = False
            return False
    
    def disconnect(self):
        """Fecha a conexão com o radar"""
        if self.sock:
            self.sock.close()
            self.connected = False
            print("Desconectado do radar")
    
    def send_command(self, command):
        """Envia um comando para o radar e retorna a resposta"""
        if not self.connected:
            print("Não conectado ao radar")
            return None
            
        # Formato CoLa A: <STX>comando<ETX>
        telegram = b'\x02' + command.encode('ascii') + b'\x03'
        
        try:
            if self.debug_mode:
                print(f"Enviando comando: {command}")
            self.sock.send(telegram)
            time.sleep(0.5)
            
            response = self.sock.recv(4096)
            return response
        except Exception as e:
            print(f"Erro ao enviar comando: {e}")
            return None
    
    def hex_to_float(self, hex_str):
        """Converte string hexadecimal IEEE-754 para float"""
        try:
            # Converte de string para valor inteiro
            value = int(hex_str, 16)
            # Converte de representação inteira para bytes
            packed = struct.pack('!I', value)
            # Desempacota como float
            unpacked = struct.unpack('!f', packed)[0]
            return unpacked
        except Exception as e:
            if self.debug_mode:
                print(f"Erro ao converter {hex_str} para float: {e}")
            return 0.0
    
    def hex_to_int(self, hex_str):
        """Converte string hexadecimal para inteiro"""
        try:
            value = int(hex_str, 16)
            # Se for maior que 32767, interpreta como número negativo (complemento de 2)
            if value > 32767:
                value -= 65536
            return value
        except Exception as e:
            if self.debug_mode:
                print(f"Erro ao converter {hex_str} para int: {e}")
            return 0
    
    def decode_angle_data(self, hex_value, scale):
        """Interpreta dados de ângulo do radar com conversão específica"""
        try:
            # Converte o valor hexadecimal em inteiro decimal
            decimal_value = int(hex_value, 16)
            
            # Correção para valores negativos (complemento de 2)
            if decimal_value > 32767:  # Se o bit mais significativo estiver ativo
                decimal_value -= 65536
            
            # Aplicar fator de escala do telegrama
            angle_value = decimal_value * scale
            
            # Verificação de codificação possível: alguns modelos usam 1/10 ou 1/32 graus
            if abs(angle_value) > 180:
                angle_value = angle_value / 32.0
                
            return angle_value
        except Exception as e:
            if self.debug_mode:
                print(f"Erro ao decodificar dados de ângulo {hex_value}: {e}")
            return 0.0
    
    def limpar_tela(self):
        """Limpa o terminal para melhor visualização"""
        os.system('cls' if os.name == 'nt' else 'clear')
    
    def collect_data(self):
        """Coleta dados do radar e extrai valores de distância, velocidade e ângulo"""
        if not self.connected:
            print("Não conectado ao radar. Conectando...")
            if not self.connect():
                return
                
        # Apenas iniciar a medição sem fazer nenhuma configuração
        print("Iniciando medição...")
        self.send_command("sEN LMDradardata 1")
        time.sleep(0.5)
        
        print("Monitorando dados do radar. Pressione Ctrl+C para parar.")
        
        try:
            # Loop principal
            while True:
                try:
                    data = self.sock.recv(8192)
                    if data:
                        # Converter para string para análise
                        data_str = data.decode('ascii', errors='replace')
                        
                        # Limpar caracteres de controle
                        clean_data = ""
                        for c in data_str:
                            if ord(c) < 32 or ord(c) > 126:
                                clean_data += " "
                            else:
                                clean_data += c
                        
                        # Dividir em tokens
                        tokens = clean_data.split()
                        
                        # Valores para armazenar os resultados
                        positions = []
                        velocities = []
                        azimuths = []
                        amplitudes = []
                        
                        # Extrair todos os blocos disponíveis
                        possible_blocks = ["P3DX1", "V3DX1", "DIST1", "VRAD1", "AZMT1", "AMPL1", "ANG1", "DIR1", "ANGLE1"]
                        
                        block_found = False
                        
                        for block_name in possible_blocks:
                            block_idx = -1
                            for i, token in enumerate(tokens):
                                if token == block_name:
                                    block_idx = i
                                    block_found = True
                                    break
                            
                            if block_idx != -1 and block_idx+3 < len(tokens):
                                # Extrair escala
                                scale_hex = tokens[block_idx+1]
                                try:
                                    scale = self.hex_to_float(scale_hex)
                                except:
                                    scale = 1.0
                                
                                # Número de valores
                                num_values = 0
                                if block_idx+3 < len(tokens):
                                    try:
                                        num_values = int(tokens[block_idx+3])
                                    except:
                                        # Tentar ler como hexadecimal se falhar como decimal
                                        try:
                                            num_values = int(tokens[block_idx+3], 16)
                                        except:
                                            num_values = 0
                                
                                if self.debug_mode:
                                    print(f"\nBloco {block_name} encontrado. Escala: {scale}, Valores: {num_values}")
                                
                                # Processar valores
                                values = []
                                for i in range(num_values):
                                    if block_idx+i+4 < len(tokens):
                                        val_hex = tokens[block_idx+i+4]
                                        
                                        try:
                                            # Converter para decimal
                                            if block_name in ["AZMT1", "ANG1", "DIR1", "ANGLE1"]:
                                                # Usar conversão especial para ângulos
                                                final_value = self.decode_angle_data(val_hex, scale)
                                                values.append(final_value)
                                                if self.debug_mode:
                                                    print(f"  {block_name}_{i+1}: HEX={val_hex} -> {final_value:.3f}°")
                                            elif block_name in ["P3DX1", "DIST1"]:
                                                # Para posições, divide por 1000 para metros
                                                decimal_value = self.hex_to_int(val_hex)
                                                final_value = decimal_value * scale / 1000.0
                                                values.append(final_value)
                                                if self.debug_mode:
                                                    print(f"  {block_name}_{i+1}: HEX={val_hex} -> DEC={decimal_value} -> {final_value:.3f}m")
                                            else:
                                                # Para velocidades e outros, usa escala direta
                                                decimal_value = self.hex_to_int(val_hex)
                                                final_value = decimal_value * scale
                                                values.append(final_value)
                                                if self.debug_mode:
                                                    print(f"  {block_name}_{i+1}: HEX={val_hex} -> DEC={decimal_value} -> {final_value:.3f}")
                                        except:
                                            if self.debug_mode:
                                                print(f"  {block_name}_{i+1}: HEX={val_hex} -> Erro na conversão")
                                
                                # Armazenar valores processados
                                if block_name in ["P3DX1", "DIST1"]:
                                    positions = values
                                elif block_name in ["V3DX1", "VRAD1"]:
                                    velocities = values
                                elif block_name in ["AZMT1", "ANG1", "DIR1", "ANGLE1"]:
                                    # Guardar valores de ângulos de qualquer bloco relacionado
                                    azimuths = values
                                elif block_name == "AMPL1":
                                    amplitudes = values
                        
                        # Limpar a tela antes de exibir os dados
                        self.limpar_tela()
                        
                        print("=" * 50)
                        print("     DADOS DO RADAR SICK RMS1000 - TEMPO REAL")
                        print("=" * 50)
                        
                        # Identificar o objeto principal (maior amplitude)
                        obj_principal = None
                        if amplitudes:
                            max_amp_index = amplitudes.index(max(amplitudes))
                            obj_principal = {
                                "amplitude": amplitudes[max_amp_index],
                                "distancia": positions[max_amp_index] if max_amp_index < len(positions) else None,
                                "velocidade": velocities[max_amp_index] if max_amp_index < len(velocities) else None,
                                "angulo": azimuths[max_amp_index] if max_amp_index < len(azimuths) else None
                            }
                        
                        # RESUMO - TODOS OS OBJETOS
                        print("\n▣ RESUMO DE TODOS OS OBJETOS DETECTADOS")
                        print("-" * 50)
                        
                        print(f"Posições detectadas: {len(positions)}")
                        for i, pos in enumerate(positions):
                            print(f"  Posição {i+1}: {pos:.3f}m")
                        
                        print(f"\nVelocidades detectadas: {len(velocities)}")
                        for i, vel in enumerate(velocities):
                            print(f"  Velocidade {i+1}: {vel:.3f}m/s")
                            
                        print(f"\nÂngulos detectados: {len(azimuths)}")
                        for i, ang in enumerate(azimuths):
                            print(f"  Ângulo {i+1}: {ang:.3f}°")
                            
                        print(f"\nAmplitudes detectadas: {len(amplitudes)}")
                        for i, amp in enumerate(amplitudes):
                            print(f"  Amplitude {i+1}: {amp:.3f}")
                        
                        # OBJETO PRINCIPAL
                        if obj_principal:
                            print("\n▣ OBJETO PRINCIPAL (MAIOR AMPLITUDE)")
                            print("-" * 50)
                            print(f"  Amplitude: {obj_principal['amplitude']:.3f}")
                            if obj_principal["distancia"] is not None:
                                print(f"  Distância: {obj_principal['distancia']:.3f}m")
                            if obj_principal["velocidade"] is not None:
                                print(f"  Velocidade: {obj_principal['velocidade']:.3f}m/s")
                            if obj_principal["angulo"] is not None:
                                print(f"  Ângulo: {obj_principal['angulo']:.3f}°")
                        
                        print("\n" + "=" * 50)
                        print("Pressione Ctrl+C para parar a monitoração")
                        
                        # Se debug_mode ativado, exibir dados brutos
                        if self.debug_mode:
                            print("\nDados brutos decodificados (primeiros 300 caracteres):")
                            print(clean_data[:300] + "..." if len(clean_data) > 300 else clean_data)
                    
                    time.sleep(0.1)
                    
                except socket.timeout:
                    pass
                except KeyboardInterrupt:
                    print("\nMonitoramento interrompido pelo usuário.")
                    break
                except Exception as e:
                    print(f"\nErro: {e}")
                    if self.debug_mode:
                        import traceback
                        traceback.print_exc()
        
        finally:
            print("Parando medição...")
            self.send_command("sEN LMDradardata 0")
            # Não desconectar automaticamente para permitir outras operações

def main():
    radar_ip = "192.168.1.84"  # IP do radar
    radar = SICKRadar(radar_ip)
    
    while True:
        print("\n===== RADAR SICK RMS1000 =====")
        print("1. Conectar ao radar")
        print("2. Coletar dados (visualização limpa)")
        print("3. Coletar dados (modo debug)")
        print("4. Desconectar")
        print("0. Sair")
        
        choice = input("\nEscolha: ")
        
        if choice == "1":
            radar.connect()
        elif choice == "2":
            radar.debug_mode = False
            radar.collect_data()
        elif choice == "3":
            radar.debug_mode = True
            radar.collect_data()
        elif choice == "4":
            radar.disconnect()
        elif choice == "0":
            if radar.connected:
                radar.disconnect()
            print("Encerrando programa...")
            break
        else:
            print("Opção inválida.")

if __name__ == "__main__":
    main()